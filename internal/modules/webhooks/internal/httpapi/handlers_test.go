package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/webhooktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type fixture struct {
	h              http.Handler
	provider       *webhooktest.Provider
	storeA, storeB tenant.StoreID
	connA, connB   ulid.ID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opt, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	ids := ulid.NewGenerator()
	lim, err := ratelimit.New(rdb, "wh-http-"+strings.ToLower(ids.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Limiter: lim, Limits: app.DefaultLimits(), Log: log})
	p := webhooktest.New()
	if err = svc.Register(p); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	httpapi.Mount(mux, svc, problem.NewResponder(log), func(context.Context) (netip.Addr, bool) {
		return netip.MustParseAddr("192.0.2.10"), true
	})
	f := fixture{h: mux, provider: p, storeA: mkStore(t, pool, ids), storeB: mkStore(t, pool, ids), connA: ids.New(), connB: ids.New()}
	p.Connect(f.storeA, f.connA, []byte("sa"))
	p.Connect(f.storeB, f.connB, []byte("sb"))
	return f
}

func mkStore(t *testing.T, pool *pgxpool.Pool, ids *ulid.Generator) tenant.StoreID {
	t.Helper()
	id := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "whh-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) post(t *testing.T, path, body, signature string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(webhooktest.SignatureHeader, signature)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out) //nolint:errcheck // asserted through out
	return rec.Code, out
}

func path(provider string, store tenant.StoreID, conn ulid.ID) string {
	return "/v1/webhooks/" + provider + "/" + store.String() + "/" + conn.String()
}

func TestReceiveWebhook(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := `{"id":"evt-77","status":"paid"}`
	sig := webhooktest.Sign([]byte("sa"), []byte(body))
	pA := path("fake", f.storeA, f.connA)

	// Happy path, then replays.
	for i, want := range []string{"accepted", "duplicate", "duplicate", "duplicate", "duplicate"} {
		code, out := f.post(t, pA, body, sig)
		if code != http.StatusOK || out["status"] != want {
			t.Fatalf("delivery %d: %d %v, want 200 %s", i+1, code, out, want)
		}
	}
	if n := len(f.provider.Accepted()); n != 1 {
		t.Fatalf("jobs = %d, want 1", n)
	}

	big := `{"id":"big","pad":"` + strings.Repeat("x", domain.MaxBody) + `"}`
	tests := []struct {
		name, path, body, sig string
		code                  int
		problem               string
	}{
		{"forged", pA, body, webhooktest.Sign([]byte("guess"), []byte(body)), http.StatusUnauthorized, ""},
		{"unsigned", pA, body, "", http.StatusUnauthorized, ""},
		{"too large", pA, big, webhooktest.Sign([]byte("sa"), []byte(big)), http.StatusBadRequest, ""},
		{"no event id", pA, `{}`, webhooktest.Sign([]byte("sa"), []byte(`{}`)), http.StatusBadRequest, ""},
		{"unknown provider", path("nobody", f.storeA, f.connA), body, sig, http.StatusNotFound, "webhook_not_found"},
		{"bad store id", "/v1/webhooks/fake/not-a-ulid/" + f.connA.String(), body, sig, http.StatusNotFound, "webhook_not_found"},
		{"bad connection id", "/v1/webhooks/fake/" + f.storeA.String() + "/nope", body, sig, http.StatusNotFound, "webhook_not_found"},
		// Cross-tenant: store A's URL with store B's connection, signed
		// with B's secret, is 404, and store B's own URL still works.
		{"B's connection under A", path("fake", f.storeA, f.connB), body, webhooktest.Sign([]byte("sb"), []byte(body)), http.StatusNotFound, "webhook_not_found"},
		{"A's connection under B", path("fake", f.storeB, f.connA), body, sig, http.StatusNotFound, "webhook_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out := f.post(t, tt.path, tt.body, tt.sig)
			if code != tt.code || (tt.problem != "" && out["code"] != tt.problem) {
				t.Fatalf("%d %v, want %d %s", code, out, tt.code, tt.problem)
			}
		})
	}
	if n := len(f.provider.Accepted()); n != 1 {
		t.Fatalf("refused deliveries enqueued jobs: %d", n)
	}
	code, out := f.post(t, path("fake", f.storeB, f.connB), body, webhooktest.Sign([]byte("sb"), []byte(body)))
	if code != http.StatusOK || out["status"] != "accepted" {
		t.Fatalf("store B's own delivery: %d %v", code, out)
	}
}
