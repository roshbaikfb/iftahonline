package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	infra       testinfra.Infra
	crossTenant testkit.Registry
)

// TestMain fails the package if any messaging operation under
// /v1/stores/{store_id} lacks a cross-tenant test (ADR-0006).
func TestMain(m *testing.M) {
	code := testinfra.Run(m, &infra)
	if code == 0 && !testing.Short() && runAll() {
		_, file, _, _ := runtime.Caller(0)
		spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "api", "openapi.yaml"))
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "load spec:", err)
			os.Exit(1)
		}
		if missing := crossTenant.Missing(testkit.StoreScopedOperations(spec, "messaging")); len(missing) > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "operations without a cross-tenant test:", missing)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

func runAll() bool {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.run=") || strings.HasPrefix(a, "-test.fuzz=") {
			return false
		}
	}
	return true
}

const (
	tokOwnerA    = "owner-a"
	tokManagerA  = "manager-a"
	tokSupportA  = "support-a"
	tokShippingA = "shipping-a"
	tokOwnerB    = "owner-b"
	textOfB      = "{{store}}: نص المتجر ب السري {{url}}"
)

type fixture struct {
	noticeB        ulid.ID
	h              http.Handler
	svc            *app.Service
	storeA, storeB tenant.StoreID
	ctxA, ctxB     context.Context
	msgB           ulid.ID
	orderB         ulid.ID
	pool           *pgxpool.Pool
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: pool, Config: config.Entitlements{MessagesPerMonth: 5}})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{pool: pool}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: ids, Keyring: k, Quota: ent.Entitlements(), Jobs: testkit.NewJobs(),
		Channels: app.Channels{WhatsApp: fake.New("fake_whatsapp", []byte("s"))}, Buttons: domain.ButtonKey("0123456789abcdef0123456789abcdef"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "mgh-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return sctx, id
	}
	f.ctxA, f.storeA = mk()
	f.ctxB, f.storeB = mk()
	f.orderB = ids.New()
	f.msgB = f.enqueue(f.ctxB, t, &f.orderB)
	if _, err = f.svc.SaveText(f.ctxB, domain.OrderConfirmation, "ar", textOfB); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.SaveSettings(f.ctxB, domain.Settings{ConfirmCOD: false, AutoCancelHours: 5}); err != nil {
		t.Fatal(err)
	}
	f.seedConfirmation(f.ctxB, t, f.orderB, f.msgB)
	f.noticeB = f.seedNotice(f.ctxB, t, f.orderB)

	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleManager:  {authz.PermMessagingRead, authz.PermMessagingManage},
		authz.RoleSupport:  {authz.PermMessagingRead},
		authz.RoleShipping: {authz.PermOrdersRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	members := map[string]testkit.FakeMember{
		tokOwnerA:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleOwner}},
		tokManagerA:  {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleManager}},
		tokSupportA:  {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleSupport}},
		tokShippingA: {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeA: authz.RoleShipping}},
		tokOwnerB:    {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{f.storeB: authz.RoleOwner}},
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, f.svc, pol, testkit.FakeStoreScope(resp, members), resp)
	f.h = mux
	return f
}

// seedConfirmation opens an order's confirmation as ConfirmOrder would.
func (f fixture) seedConfirmation(ctx context.Context, t *testing.T, order, msg ulid.ID) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO order_confirmations (store_id, order_id, message_id, status, expires_at)
			VALUES (app.current_store_id(), $1, $2, 'pending', now() + interval '24 hours')`, order, msg)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// seedNotice adds an order's notice.
func (f fixture) seedNotice(ctx context.Context, t *testing.T, order ulid.ID) ulid.ID {
	t.Helper()
	id := ulid.NewGenerator().New()
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO merchant_notifications (store_id, id, kind, dedup_key, data)
			VALUES (app.current_store_id(), $1, 'order_confirmed', $2, jsonb_build_object('order_id', $2::text, 'order_ref', 'REF'))`, id, order.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) enqueue(ctx context.Context, t *testing.T, order *ulid.ID) ulid.ID {
	t.Helper()
	to, err := phone.Parse("01012345678", phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	var q app.Queued
	if err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		q, txErr = f.svc.EnqueueTx(ctx, tx, app.Outgoing{Kind: domain.OrderConfirmation, Locale: "ar", OrderID: order, To: to,
			Vars: domain.Vars{CustomerName: "منى", StoreName: "سوق", OrderRef: "7P0R", Total: "520.00 EGP"}})
		return txErr
	}); err != nil {
		t.Fatal(err)
	}
	return q.ID
}

func req(t *testing.T, method, path, token, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rq := httptest.NewRequestWithContext(t.Context(), method, path, r)
	if token != "" {
		rq.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		rq.Header.Set("Content-Type", "application/json")
	}
	return rq
}

func (f fixture) do(t *testing.T, rq *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, m
}

func path(s tenant.StoreID, rest string) string { return "/v1/stores/" + s.String() + rest }

func TestHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	order := ulid.NewGenerator().New()
	id := f.enqueue(f.ctxA, t, &order)
	f.enqueue(f.ctxA, t, nil)

	code, page := f.do(t, req(t, http.MethodGet, path(a, "/messages"), tokSupportA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 2 || page["next_cursor"] != nil {
		t.Fatalf("listMessages = %d %v", code, page)
	}
	item := page["items"].([]any)[1].(map[string]any)
	if item["id"] != id.String() || item["status"] != "queued" || item["kind"] != "order_confirmation" || item["locale"] != "ar" ||
		item["to"] != "+2010******78" || item["order_id"] != order.String() || item["fallback"] != false || item["channel"] != nil {
		t.Fatalf("message %v", item)
	}
	code, page = f.do(t, req(t, http.MethodGet, path(a, "/messages?order_id="+order.String()), tokSupportA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 1 {
		t.Fatalf("by order = %d %v", code, page)
	}
	if code, page = f.do(t, req(t, http.MethodGet, path(a, "/messages?status=sent"), tokSupportA, "")); code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("by status = %d %v", code, page)
	}

	code, u := f.do(t, req(t, http.MethodGet, path(a, "/messaging/usage"), tokSupportA, ""))
	if code != http.StatusOK || u["used"] != float64(2) || u["limit"] != float64(5) || u["remaining"] != float64(3) || u["level"] != "ok" ||
		!strings.HasSuffix(u["period"].(string), "-01") {
		t.Fatalf("getMessageUsage = %d %v", code, u)
	}

	code, texts := f.do(t, req(t, http.MethodGet, path(a, "/messaging/texts"), tokSupportA, ""))
	if code != http.StatusOK || len(texts["items"].([]any)) != 2 {
		t.Fatalf("listMessageTexts = %d %v", code, texts)
	}
	first := texts["items"].([]any)[0].(map[string]any)
	if first["custom"] != false || first["locale"] != "ar" || !strings.Contains(first["sms_body"].(string), "{{url}}") {
		t.Fatalf("default text %v", first)
	}
	body := `{"sms_body":"{{store}}: يا {{name}} أكّد طلب {{ref}} من {{url}}"}`
	code, saved := f.do(t, req(t, http.MethodPut, path(a, "/messaging/texts/order_confirmation/ar"), tokManagerA, body))
	if code != http.StatusOK || saved["custom"] != true || saved["sms_body"] != "{{store}}: يا {{name}} أكّد طلب {{ref}} من {{url}}" || saved["updated_at"] == nil {
		t.Fatalf("saveMessageText = %d %v", code, saved)
	}
	_, texts = f.do(t, req(t, http.MethodGet, path(a, "/messaging/texts"), tokSupportA, ""))
	if texts["items"].([]any)[0].(map[string]any)["custom"] != true || texts["items"].([]any)[1].(map[string]any)["custom"] != false {
		t.Fatalf("after save %v", texts)
	}
	code, reset := f.do(t, req(t, http.MethodDelete, path(a, "/messaging/texts/order_confirmation/ar"), tokManagerA, ""))
	if code != http.StatusOK || reset["custom"] != false || reset["sms_body"] != domain.DefaultSMS(domain.OrderConfirmation, "ar") {
		t.Fatalf("resetMessageText = %d %v", code, reset)
	}
	// Resetting a default text is fine.
	if code, _ = f.do(t, req(t, http.MethodDelete, path(a, "/messaging/texts/order_confirmation/en"), tokManagerA, "")); code != http.StatusOK {
		t.Fatalf("reset of a default = %d", code)
	}
}

// TestSMSUsesTheStoreText: the send job writes the store's own text.
func TestSMSUsesTheStoreText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sms := fake.New("fake_sms", []byte("s"))
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Config: config.Entitlements{MessagesPerMonth: 5}})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: f.pool, IDs: ulid.NewGenerator(), Keyring: k, Quota: ent.Entitlements(), Jobs: testkit.NewJobs(),
		Channels: app.Channels{SMS: sms}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err = svc.SendMessage(f.ctxB, f.msgB, false); err != nil {
		t.Fatal(err)
	}
	if got := sms.Sent(); len(got) != 1 || got[0].Text != "سوق: نص المتجر ب السري " {
		t.Fatalf("SMS = %+v", got)
	}
}

func TestPaging(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	for range 3 {
		f.enqueue(f.ctxA, t, nil)
	}
	seen := 0
	cursor := ""
	for range 3 {
		code, page := f.do(t, req(t, http.MethodGet, path(a, "/messages?limit=2"+cursor), tokOwnerA, ""))
		if code != http.StatusOK {
			t.Fatalf("page = %d %v", code, page)
		}
		seen += len(page["items"].([]any))
		next, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = "&cursor=" + next
	}
	if seen != 3 {
		t.Fatalf("paged through %d messages, want 3", seen)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	text := "/messaging/texts/order_confirmation/ar"
	tests := []struct {
		name, method, path, body string
		want                     int
		field                    string
	}{
		{"no link", http.MethodPut, text, `{"sms_body":"{{store}} confirm please"}`, http.StatusUnprocessableEntity, "sms_body"},
		{"unknown placeholder", http.MethodPut, text, `{"sms_body":"{{url}} {{phone}}"}`, http.StatusUnprocessableEntity, "sms_body"},
		{"too long", http.MethodPut, text, `{"sms_body":"{{url}}` + strings.Repeat("ا", 400) + `"}`, http.StatusUnprocessableEntity, "sms_body"},
		{"empty", http.MethodPut, text, `{"sms_body":""}`, http.StatusUnprocessableEntity, "sms_body"},
		{"no body", http.MethodPut, text, ``, http.StatusBadRequest, ""},
		{"bad json", http.MethodPut, text, `{"sms_body":`, http.StatusBadRequest, ""},
		{"unknown kind", http.MethodPut, "/messaging/texts/promo/ar", `{"sms_body":"{{url}}"}`, http.StatusNotFound, ""},
		{"unknown locale", http.MethodPut, "/messaging/texts/order_confirmation/fr", `{"sms_body":"{{url}}"}`, http.StatusNotFound, ""},
		{"reset unknown locale", http.MethodDelete, "/messaging/texts/order_confirmation/fr", ``, http.StatusNotFound, ""},
		{"bad cursor", http.MethodGet, "/messages?cursor=nope", ``, http.StatusUnprocessableEntity, "cursor"},
		{"limit too big", http.MethodGet, "/messages?limit=500", ``, http.StatusUnprocessableEntity, "limit"},
		{"unknown status", http.MethodGet, "/messages?status=lost", ``, http.StatusUnprocessableEntity, "status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := f.do(t, req(t, tt.method, path(a, tt.path), tokOwnerA, tt.body))
			if code != tt.want {
				t.Fatalf("%s %s = %d %v, want %d", tt.method, tt.path, code, body, tt.want)
			}
			if tt.field != "" && !strings.Contains(fmt.Sprint(body["errors"]), "field:"+tt.field+" ") {
				t.Fatalf("errors = %v, want one on %s", body["errors"], tt.field)
			}
		})
	}
	if code, page := f.do(t, req(t, http.MethodGet, path(a, "/messages?order_id=nope"), tokOwnerA, "")); code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("malformed order filter = %d %v", code, page)
	}
}

// TestAuthorization: support reads; only the owner and manager write
// texts; a role without messaging.read sees nothing; no token is 401.
func TestAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	text := "/messaging/texts/order_confirmation/ar"
	body := `{"sms_body":"{{url}}"}`
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{tokSupportA, http.MethodPut, text, body, http.StatusForbidden},
		{tokSupportA, http.MethodDelete, text, ``, http.StatusForbidden},
		{tokShippingA, http.MethodGet, "/messages", ``, http.StatusForbidden},
		{tokShippingA, http.MethodGet, "/messaging/usage", ``, http.StatusForbidden},
		{tokShippingA, http.MethodGet, "/messaging/texts", ``, http.StatusForbidden},
		{"", http.MethodGet, "/messages", ``, http.StatusUnauthorized},
		{"", http.MethodPut, text, body, http.StatusUnauthorized},
		{tokManagerA, http.MethodPut, text, body, http.StatusOK},
		{tokSupportA, http.MethodGet, "/messaging/usage", ``, http.StatusOK},
	}
	for _, tt := range tests {
		if code, b := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s %s %s = %d %v, want %d", tt.token, tt.method, tt.path, code, b, tt.want)
		}
	}
}

func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, b := f.storeA, f.storeB
	leaks := []string{f.msgB.String(), f.orderB.String(), "نص المتجر ب السري"}
	unchanged := func(t testing.TB) {
		t.Helper()
		texts, err := f.svc.Texts(f.ctxB)
		if err != nil || !texts[0].Custom || texts[0].SMSBody != textOfB {
			t.Fatalf("store B's text changed: %+v %v", texts, err)
		}
	}
	text := "/messaging/texts/order_confirmation/ar"
	unchangedB := func(t testing.TB) {
		t.Helper()
		unchanged(t)
		if st, err := f.svc.Settings(f.ctxB); err != nil || st.ConfirmCOD || st.AutoCancelHours != 5 {
			t.Fatalf("store B's settings changed: %+v %v", st, err)
		}
		if p, err := f.svc.Notifications(f.ctxB, nil, true, 10); err != nil || p.Unread != 1 {
			t.Fatalf("store B's notices changed: %+v %v", p, err)
		}
	}
	leaks = append(leaks, f.noticeB.String())
	read := `{"ids":["` + f.noticeB.String() + `"]}`
	cases := []testkit.CrossTenantCase{
		{OperationID: "getMessagingSettings", Request: req(t, http.MethodGet, path(b, "/messaging/settings"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "updateMessagingSettings", Request: req(t, http.MethodPut, path(b, "/messaging/settings"), tokOwnerA, `{"confirm_cod":true,"auto_cancel_hours":1}`),
			Leaks: leaks, Unchanged: unchangedB},
		{OperationID: "getOrderConfirmation", Request: req(t, http.MethodGet, path(b, "/messaging/confirmations/"+f.orderB.String()), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getOrderConfirmation", Request: req(t, http.MethodGet, path(a, "/messaging/confirmations/"+f.orderB.String()), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "listNotifications", Request: req(t, http.MethodGet, path(b, "/messaging/notifications"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "markNotificationsRead", Request: req(t, http.MethodPost, path(b, "/messaging/notifications/read"), tokOwnerA, read), Leaks: leaks, Unchanged: unchangedB},
		{OperationID: "listMessages", Request: req(t, http.MethodGet, path(b, "/messages"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "getMessageUsage", Request: req(t, http.MethodGet, path(b, "/messaging/usage"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "listMessageTexts", Request: req(t, http.MethodGet, path(b, "/messaging/texts"), tokOwnerA, ""), Leaks: leaks},
		{OperationID: "saveMessageText", Request: req(t, http.MethodPut, path(b, text), tokOwnerA, `{"sms_body":"{{url}} مزوّر"}`), Leaks: leaks, Unchanged: unchanged},
		{OperationID: "resetMessageText", Request: req(t, http.MethodDelete, path(b, text), tokOwnerA, ""), Leaks: leaks, Unchanged: unchanged},
	}
	for _, c := range cases {
		t.Run(c.OperationID+" "+c.Request.Method+" "+c.Request.URL.Path, func(t *testing.T) {
			testkit.AssertCrossTenantDenied(t, &crossTenant, f.h, c)
		})
	}
	// A's own log, filtered by B's order, is empty; A's texts are defaults.
	code, page := f.do(t, req(t, http.MethodGet, path(a, "/messages?order_id="+f.orderB.String()), tokOwnerA, ""))
	if code != http.StatusOK || len(page["items"].([]any)) != 0 {
		t.Fatalf("A's messages of B's order = %d %v", code, page)
	}
	if _, texts := f.do(t, req(t, http.MethodGet, path(a, "/messaging/texts"), tokOwnerA, "")); texts["items"].([]any)[0].(map[string]any)["custom"] != false {
		t.Fatalf("A sees B's text: %v", texts)
	}
	if code, page = f.do(t, req(t, http.MethodGet, path(b, "/messages"), tokOwnerB, "")); code != http.StatusOK || len(page["items"].([]any)) != 1 {
		t.Fatalf("B's messages = %d %v", code, page)
	}
	// A marking B's notice in A's own store: nothing of A's, B's unread.
	if code, out := f.do(t, req(t, http.MethodPost, path(a, "/messaging/notifications/read"), tokOwnerA, read)); code != http.StatusOK || out["marked"] != float64(0) {
		t.Fatalf("A marks B's notice = %d %v", code, out)
	}
	unchangedB(t)
}

func TestSettingsConfirmationsAndNotices(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.storeA
	code, s := f.do(t, req(t, http.MethodGet, path(a, "/messaging/settings"), tokSupportA, ""))
	if code != http.StatusOK || s["confirm_cod"] != true || s["auto_cancel_hours"] != float64(24) {
		t.Fatalf("defaults = %d %v", code, s)
	}
	code, s = f.do(t, req(t, http.MethodPut, path(a, "/messaging/settings"), tokManagerA, `{"confirm_cod":false,"auto_cancel_hours":72}`))
	if code != http.StatusOK || s["confirm_cod"] != false || s["auto_cancel_hours"] != float64(72) {
		t.Fatalf("update = %d %v", code, s)
	}
	if _, s = f.do(t, req(t, http.MethodGet, path(a, "/messaging/settings"), tokSupportA, "")); s["auto_cancel_hours"] != float64(72) {
		t.Fatalf("after update = %v", s)
	}

	order := ulid.NewGenerator().New()
	msg := f.enqueue(f.ctxA, t, &order)
	f.seedConfirmation(f.ctxA, t, order, msg)
	code, c := f.do(t, req(t, http.MethodGet, path(a, "/messaging/confirmations/"+order.String()), tokSupportA, ""))
	if code != http.StatusOK || c["status"] != "pending" || c["message_id"] != msg.String() || c["order_id"] != order.String() || c["expires_at"] == nil {
		t.Fatalf("confirmation = %d %v", code, c)
	}

	first := f.seedNotice(f.ctxA, t, ulid.NewGenerator().New())
	second := f.seedNotice(f.ctxA, t, order)
	code, page := f.do(t, req(t, http.MethodGet, path(a, "/messaging/notifications?limit=1"), tokSupportA, ""))
	items := page["items"].([]any)
	if code != http.StatusOK || page["unread"] != float64(2) || len(items) != 1 || items[0].(map[string]any)["id"] != second.String() ||
		items[0].(map[string]any)["order_ref"] != "REF" || items[0].(map[string]any)["read"] != false || page["next_cursor"] == nil {
		t.Fatalf("notices = %d %v", code, page)
	}
	_, page = f.do(t, req(t, http.MethodGet, path(a, "/messaging/notifications?cursor="+page["next_cursor"].(string)), tokSupportA, ""))
	if items = page["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != first.String() {
		t.Fatalf("page 2 = %v", page)
	}
	code, out := f.do(t, req(t, http.MethodPost, path(a, "/messaging/notifications/read"), tokSupportA, `{"ids":["`+second.String()+`","nope"]}`))
	if code != http.StatusOK || out["marked"] != float64(1) {
		t.Fatalf("mark read = %d %v", code, out)
	}
	_, page = f.do(t, req(t, http.MethodGet, path(a, "/messaging/notifications?unread_only=true"), tokSupportA, ""))
	if items = page["items"].([]any); page["unread"] != float64(1) || len(items) != 1 || items[0].(map[string]any)["id"] != first.String() {
		t.Fatalf("unread only = %v", page)
	}

	tests := []struct {
		name, method, path, body, token string
		want                            int
	}{
		{"hours 0", http.MethodPut, "/messaging/settings", `{"confirm_cod":true,"auto_cancel_hours":0}`, tokOwnerA, http.StatusUnprocessableEntity},
		{"hours 73", http.MethodPut, "/messaging/settings", `{"confirm_cod":true,"auto_cancel_hours":73}`, tokOwnerA, http.StatusUnprocessableEntity},
		{"no settings body", http.MethodPut, "/messaging/settings", ``, tokOwnerA, http.StatusBadRequest},
		{"support writes settings", http.MethodPut, "/messaging/settings", `{"confirm_cod":true,"auto_cancel_hours":5}`, tokSupportA, http.StatusForbidden},
		{"shipping reads settings", http.MethodGet, "/messaging/settings", ``, tokShippingA, http.StatusForbidden},
		{"shipping reads notices", http.MethodGet, "/messaging/notifications", ``, tokShippingA, http.StatusForbidden},
		{"shipping marks notices", http.MethodPost, "/messaging/notifications/read", `{"ids":["` + first.String() + `"]}`, tokShippingA, http.StatusForbidden},
		{"no confirmation", http.MethodGet, "/messaging/confirmations/" + ulid.NewGenerator().New().String(), ``, tokOwnerA, http.StatusNotFound},
		{"malformed order", http.MethodGet, "/messaging/confirmations/nope", ``, tokOwnerA, http.StatusNotFound},
		{"bad notice cursor", http.MethodGet, "/messaging/notifications?cursor=nope", ``, tokOwnerA, http.StatusUnprocessableEntity},
		{"notice limit", http.MethodGet, "/messaging/notifications?limit=500", ``, tokOwnerA, http.StatusUnprocessableEntity},
		{"no ids", http.MethodPost, "/messaging/notifications/read", `{"ids":[]}`, tokOwnerA, http.StatusUnprocessableEntity},
		{"no read body", http.MethodPost, "/messaging/notifications/read", ``, tokOwnerA, http.StatusBadRequest},
		{"no token", http.MethodGet, "/messaging/notifications", ``, "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		if code, body := f.do(t, req(t, tt.method, path(a, tt.path), tt.token, tt.body)); code != tt.want {
			t.Errorf("%s: %d %v, want %d", tt.name, code, body, tt.want)
		}
	}
}
