package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

func TestImpersonationSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, _ := f.signup(t)
	b, _ := f.signup(t)
	admin := f.ids.New()
	inA := tenant.WithStore(f.device(t), a.StoreID)

	g, err := f.svc.StartImpersonation(inA, admin, "ticket 7", 10*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	token, expires, err := f.svc.ExchangeImpersonation(f.device(t), g.Code)
	if err != nil || expires.Sub(g.ExpiresAt).Abs() > time.Millisecond {
		t.Fatalf("exchange = %v %v (grant %v)", expires, err, g.ExpiresAt)
	}
	if _, _, err = f.svc.ExchangeImpersonation(f.device(t), g.Code); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("second exchange: %v", err)
	}
	for _, code := range []string{"junk", string(a.Token)} {
		if _, _, err = f.svc.ExchangeImpersonation(f.device(t), code); !errs.Is(err, errs.KindUnauthenticated) {
			t.Fatalf("code %q: %v", code, err)
		}
	}

	// In its store: the owner, marked with the admin and read-only.
	p, err := f.svc.AuthenticateInStore(inA, string(token))
	if err != nil || p.UserID != a.User.ID || p.Role != authz.RoleOwner || p.ImpersonatedBy != admin || !p.ReadOnly {
		t.Fatalf("principal = %+v, %v", p, err)
	}
	// Never in another store, even one where the owner is a member.
	if err = pgx.BeginFunc(t.Context(), f.owner, func(tx pgx.Tx) error {
		if _, txErr := tx.Exec(t.Context(), "SELECT set_config('app.store_id', $1::uuid::text, true)", b.StoreID.ID); txErr != nil {
			return txErr
		}
		_, txErr := tx.Exec(t.Context(), "INSERT INTO store_memberships (store_id, user_id, role) VALUES ($1, $2, 'manager')", b.StoreID.ID, a.User.ID)
		return txErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), b.StoreID), string(token)); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("other store: %v", err)
	}

	// Outside a store: the identity carries the impersonation and /me
	// lists only its store.
	id, err := f.svc.Authenticate(t.Context(), string(token))
	if err != nil || id.Impersonation == nil || id.Impersonation.Admin != admin || id.Impersonation.Store != a.StoreID || !id.Impersonation.ReadOnly {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	_, ms, err := f.svc.Me(app.WithIdentity(t.Context(), id))
	if err != nil || len(ms) != 1 || ms[0].StoreID != a.StoreID {
		t.Fatalf("memberships = %+v, %v", ms, err)
	}

	// Use never slides its expiry.
	if _, err = f.owner.Exec(t.Context(), "UPDATE user_sessions SET last_seen_at = now() - interval '1 hour' WHERE impersonator_id = $1", admin); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Authenticate(t.Context(), string(token)); err != nil {
		t.Fatal(err)
	}
	var left time.Duration
	if err = f.owner.QueryRow(t.Context(), "SELECT expires_at - now() FROM user_sessions WHERE impersonator_id = $1", admin).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left > 10*time.Minute {
		t.Fatalf("expiry slid: %v left", left)
	}

	// Every entry written during the session names the admin; the start
	// and a request are recorded.
	ctx := authz.WithPrincipal(inA, p)
	if err = postgres.InTenantTx(ctx, f.app, func(tx pgx.Tx) error {
		return app.NewRecorder(f.ids).RecordTx(ctx, tx, audit.Entry{Action: "store.updated", TargetType: "store", TargetID: a.StoreID.String()})
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RecordImpersonatedRequest(t.Context(), *id.Impersonation, "GET", "GET /v1/stores/{store_id}", false); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.ListAudit(tenant.WithStore(t.Context(), a.StoreID), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range page.Items {
		if string(e.Metadata) != "" && strings.Contains(string(e.Metadata), admin.String()) {
			seen[e.Action] = true
		}
	}
	if !seen["impersonation.started"] || !seen["impersonation.request"] || !seen["store.updated"] {
		t.Fatalf("entries naming the admin = %v", seen)
	}
}

func TestImpersonationRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, _ := f.signup(t)
	inA := tenant.WithStore(t.Context(), a.StoreID)
	admin := f.ids.New()
	for _, d := range []time.Duration{0, 4 * time.Minute, 61 * time.Minute} {
		if _, err := f.svc.StartImpersonation(inA, admin, "r", d, false); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%v: %v", d, err)
		}
	}
	if _, err := f.svc.StartImpersonation(inA, admin, "", 10*time.Minute, false); err == nil {
		t.Error("no reason must fail")
	}
	if _, err := f.svc.StartImpersonation(tenant.WithStore(t.Context(), tenant.StoreID{ID: f.ids.New()}), admin, "r", 10*time.Minute, false); !errs.Is(err, errs.KindConflict) {
		t.Errorf("no owner: %v", err)
	}
	if !errs.Is(app.ErrImpersonationReadOnly(), errs.KindForbidden) {
		t.Error("read-only must be 403")
	}
	// A write session is not read-only and expires at its deadline.
	g, err := f.svc.StartImpersonation(inA, admin, "r", 5*time.Minute, true)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := f.svc.ExchangeImpersonation(f.device(t), g.Code)
	if err != nil {
		t.Fatal(err)
	}
	if p, authErr := f.svc.AuthenticateInStore(inA, string(token)); authErr != nil || p.ReadOnly {
		t.Fatalf("write principal = %+v, %v", p, authErr)
	}
	if _, err = f.owner.Exec(t.Context(), "UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE impersonator_id = $1", admin); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AuthenticateInStore(inA, string(token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("expired: %v", err)
	}
}
