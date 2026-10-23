package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/webhooktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// syncBuffer is a log sink safe for concurrent handlers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	svc      *app.Service
	pool     *pgxpool.Pool
	provider *webhooktest.Provider
	log      *syncBuffer
	storeA   tenant.StoreID
	storeB   tenant.StoreID
	connA    ulid.ID
	connB    ulid.ID
	secretA  []byte
	ip       netip.Addr
}

var testIP = netip.MustParseAddr("198.51.100.7")

func newFixture(t *testing.T, limits *app.Limits) fixture {
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
	lim, err := ratelimit.New(rdb, "wh-test-"+strings.ToLower(ids.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	l := app.DefaultLimits()
	if limits != nil {
		l = *limits
	}
	logs := &syncBuffer{}
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Limiter: lim, Limits: l, Log: slog.New(slog.NewJSONHandler(logs, nil))})
	p := webhooktest.New()
	if err = svc.Register(p); err != nil {
		t.Fatal(err)
	}
	f := fixture{svc: svc, pool: pool, provider: p, log: logs, storeA: mkStore(t, pool, ids), storeB: mkStore(t, pool, ids),
		connA: ids.New(), connB: ids.New(), secretA: []byte("secret-a"), ip: testIP}
	p.Connect(f.storeA, f.connA, f.secretA)
	p.Connect(f.storeB, f.connB, []byte("secret-b"))
	return f
}

func mkStore(t *testing.T, pool *pgxpool.Pool, ids *ulid.Generator) tenant.StoreID {
	t.Helper()
	id := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "wh-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func signed(secret []byte, body string) app.Request {
	h := http.Header{}
	h.Set(webhooktest.SignatureHeader, webhooktest.Sign(secret, []byte(body)))
	h.Set("Content-Type", "application/json")
	return app.Request{Header: h, Body: []byte(body)}
}

func (f fixture) receiptCount(t *testing.T, store tenant.StoreID) int {
	t.Helper()
	ctx := tenant.WithStore(t.Context(), store)
	var n int
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM webhook_receipts) + (SELECT count(*) FROM webhook_bodies)`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestReplayedFiveTimesAcceptedOnce: the acceptance criterion at the
// inbox: five deliveries of one provider event are one receipt, one body
// and one processing job, sequentially and concurrently.
func TestReplayedFiveTimesAcceptedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	body := `{"id":"evt-1","state":"delivered"}`

	var dups int
	for range 5 {
		dup, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, body), f.ip)
		if err != nil {
			t.Fatal(err)
		}
		if dup {
			dups++
		}
	}
	if dups != 4 || len(f.provider.Accepted()) != 1 || f.receiptCount(t, f.storeA) != 2 {
		t.Fatalf("duplicates = %d (want 4), jobs = %d (want 1), rows = %d (want receipt + body)",
			dups, len(f.provider.Accepted()), f.receiptCount(t, f.storeA))
	}

	r := f.provider.Accepted()[0]
	got, ct, raw, err := f.svc.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EventID != "evt-1" || got.Provider != "fake" || got.ConnectionID != f.connA || ct != "application/json" || string(raw) != body {
		t.Fatalf("Get = %+v %q %q", got, ct, raw)
	}
	if !got.ReceivedAt.Equal(domain.ReceivedAtOf(got.ID)) || got.ProcessedAt != nil {
		t.Fatalf("receipt times: %+v", got)
	}

	var wg sync.WaitGroup
	results := make(chan bool, 5)
	for range 5 {
		wg.Go(func() {
			dup, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"evt-2"}`), f.ip)
			if err != nil {
				t.Error(err)
			}
			results <- dup
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for dup := range results {
		if !dup {
			accepted++
		}
	}
	if accepted != 1 || len(f.provider.Accepted()) != 2 {
		t.Fatalf("concurrent: accepted = %d, jobs = %d; want 1 and 2", accepted, len(f.provider.Accepted()))
	}
}

// TestForgedRejectedAndLogged: a bad signature stores nothing, is 401,
// and is logged with the body's hash, never the body.
func TestForgedRejectedAndLogged(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	body := `{"id":"evt-forged","note":"attacker-text"}`
	cases := map[string]app.Request{
		"wrong secret":      signed([]byte("guess"), body),
		"no signature":      {Header: http.Header{}, Body: []byte(body)},
		"not hex":           {Header: http.Header{webhooktest.SignatureHeader: {"zz"}}, Body: []byte(body)},
		"body changed":      {Header: signed(f.secretA, `{"id":"evt-forged"}`).Header, Body: []byte(body)},
		"other store's key": signed([]byte("secret-b"), body),
	}
	for name, req := range cases {
		_, err := f.svc.Receive(ctx, "fake", f.connA, req, f.ip)
		if !errors.Is(err, app.ErrForged) || errs.KindOf(err) != errs.KindUnauthenticated {
			t.Errorf("%s: err = %v, want ErrForged (401)", name, err)
		}
	}
	if n := f.receiptCount(t, f.storeA); n != 0 || len(f.provider.Accepted()) != 0 {
		t.Fatalf("forged deliveries stored %d rows, %d jobs", n, len(f.provider.Accepted()))
	}
	logs := f.log.String()
	if strings.Count(logs, `"msg":"webhook rejected"`) != len(cases) || !strings.Contains(logs, `"reason":"signature"`) ||
		!strings.Contains(logs, `"body_sha256":"`) || !strings.Contains(logs, `"ip_prefix":"198.51.100.0/24"`) {
		t.Fatalf("rejections not logged as expected:\n%s", logs)
	}
	if strings.Contains(logs, "attacker-text") || strings.Contains(logs, "secret-a") {
		t.Fatalf("log carries the body or a secret:\n%s", logs)
	}
}

// TestCrossTenant: a connection belongs to one store; the same event id
// in two stores is two events; a receipt is invisible to other stores.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctxA := tenant.WithStore(t.Context(), f.storeA)
	ctxB := tenant.WithStore(t.Context(), f.storeB)

	// Store A's path with store B's connection (and B's valid signature).
	if _, err := f.svc.Receive(ctxA, "fake", f.connB, signed([]byte("secret-b"), `{"id":"x"}`), f.ip); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("B's connection under A: %v, want 404", err)
	}
	for _, c := range []struct {
		ctx    context.Context
		conn   ulid.ID
		secret []byte
	}{{ctxA, f.connA, f.secretA}, {ctxB, f.connB, []byte("secret-b")}} {
		if dup, err := f.svc.Receive(c.ctx, "fake", c.conn, signed(c.secret, `{"id":"same-id"}`), f.ip); err != nil || dup {
			t.Fatalf("same event id in another store: dup=%v err=%v", dup, err)
		}
	}
	rA := f.provider.Accepted()[0]
	if _, _, _, err := f.svc.Get(ctxB, rA.ID); !errors.Is(err, app.ErrReceiptAbsent) {
		t.Fatalf("B reads A's receipt: %v", err)
	}
	if err := postgres.InTenantTx(ctxB, f.pool, func(tx pgx.Tx) error {
		recorded, err := f.svc.MarkProcessedTx(ctxB, tx, rA.ID, "paid")
		if recorded {
			return errors.New("B marked A's receipt")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRefusals: unknown provider and connection are 404, a verified body
// without an event id is 400; none stores anything.
func TestRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	if _, err := f.svc.Receive(ctx, "nobody", f.connA, signed(f.secretA, `{"id":"a"}`), f.ip); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := f.svc.Receive(ctx, "fake", ulid.NewGenerator().New(), signed(f.secretA, `{"id":"a"}`), f.ip); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown connection: %v", err)
	}
	for _, body := range []string{`{}`, `{"id":"has space"}`, fmt.Sprintf(`{"id":%q}`, strings.Repeat("x", 201)), `not json`} {
		if _, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, body), f.ip); !errors.Is(err, app.ErrBadEventID) {
			t.Errorf("body %.30q: %v, want ErrBadEventID", body, err)
		}
	}
	if _, err := f.svc.Receive(t.Context(), "fake", f.connA, signed(f.secretA, `{"id":"a"}`), f.ip); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("no tenant: %v", err)
	}
	if n := f.receiptCount(t, f.storeA); n != 0 {
		t.Fatalf("refusals stored %d rows", n)
	}
	if !strings.Contains(f.log.String(), `"reason":"unknown_connection"`) {
		t.Error("unknown connection not logged")
	}
}

// verifyErr is a provider that cannot decide (its key holder is down).
type verifyErr struct{ *webhooktest.Provider }

func (verifyErr) Name() string { return "flaky" }
func (verifyErr) Verify(context.Context, ulid.ID, app.Request) (domain.Verified, error) {
	return domain.Verified{}, errors.New("kms unreachable")
}

// TestAcceptTxIsTheSameTransaction: when enqueueing the job fails, the
// receipt rolls back, so the provider's retry is accepted.
func TestAcceptTxIsTheSameTransaction(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	f.provider.FailAccept = errors.New("queue down")
	if _, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"e"}`), f.ip); err == nil {
		t.Fatal("AcceptTx failure swallowed")
	}
	if n := f.receiptCount(t, f.storeA); n != 0 {
		t.Fatalf("receipt survived its job's failure: %d rows", n)
	}
	f.provider.FailAccept = nil
	if dup, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"e"}`), f.ip); err != nil || dup {
		t.Fatalf("retry: dup=%v err=%v", dup, err)
	}

	if err := f.svc.Register(verifyErr{webhooktest.New()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Receive(ctx, "flaky", f.connA, signed(f.secretA, `{"id":"e"}`), f.ip); err == nil ||
		errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("undecided verify: %v, want an internal error (provider retries)", err)
	}
}

func TestRegister(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if err := f.svc.Register(webhooktest.New()); err == nil {
		t.Fatal("second provider named fake accepted")
	}
	if err := f.svc.Register(badName{webhooktest.New()}); err == nil {
		t.Fatal("invalid provider name accepted")
	}
}

type badName struct{ *webhooktest.Provider }

func (badName) Name() string { return "Bad Name" }

func TestRateLimits(t *testing.T) {
	t.Parallel()
	f := newFixture(t, &app.Limits{
		PerIP:         ratelimit.Limit{Burst: 3, Period: time.Hour},
		PerConnection: ratelimit.Limit{Burst: 2, Period: time.Hour},
	})
	ctx := tenant.WithStore(t.Context(), f.storeA)
	for i := range 2 {
		if _, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, fmt.Sprintf(`{"id":"r%d"}`, i)), f.ip); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"r9"}`), f.ip); errs.KindOf(err) != errs.KindRateLimited {
		t.Fatalf("per connection: %v, want 429", err)
	}
	ctxB := tenant.WithStore(t.Context(), f.storeB)
	if _, err := f.svc.Receive(ctxB, "fake", f.connB, signed([]byte("secret-b"), `{"id":"b"}`), f.ip); errs.KindOf(err) != errs.KindRateLimited {
		t.Fatalf("per IP: %v, want 429", err)
	}
	if _, err := f.svc.Receive(ctxB, "fake", f.connB, signed([]byte("secret-b"), `{"id":"b"}`), netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("another IP: %v", err)
	}
}

func TestMarkProcessed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	if _, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"m"}`), f.ip); err != nil {
		t.Fatal(err)
	}
	id := f.provider.Accepted()[0].ID
	for i, outcome := range []string{"paid", "stale"} {
		var recorded bool
		if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (err error) {
			recorded, err = f.svc.MarkProcessedTx(ctx, tx, id, outcome)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if recorded != (i == 0) {
			t.Fatalf("%s: recorded = %v", outcome, recorded)
		}
	}
	r, _, _, err := f.svc.Get(ctx, id)
	if err != nil || r.Outcome != "paid" || r.ProcessedAt == nil {
		t.Fatalf("receipt = %+v, %v; want the first outcome", r, err)
	}
	if err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, markErr := f.svc.MarkProcessedTx(ctx, tx, id, "Not Valid")
		return markErr
	}); err == nil {
		t.Fatal("invalid outcome accepted")
	}
}

// TestReceiptsExpire: each accepted webhook purges its store's receipts
// older than the retention, and only its own store's.
func TestReceiptsExpire(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	old := time.Now().Add(-domain.ReceiptRetention - time.Hour)
	ids := ulid.NewGeneratorWith(func() time.Time { return old }, rand.Reader)
	insertOld := func(store tenant.StoreID, event string) {
		ctx := tenant.WithStore(t.Context(), store)
		if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO webhook_receipts (id, store_id, provider, connection_id, event_id, received_at)
				VALUES ($1, $2, 'fake', $3, $4, $5)`, ids.New(), store.ID, f.connA, event, old)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	insertOld(f.storeA, "old-a")
	insertOld(f.storeB, "old-b")
	ctx := tenant.WithStore(t.Context(), f.storeA)
	if dup, err := f.svc.Receive(ctx, "fake", f.connA, signed(f.secretA, `{"id":"old-a"}`), f.ip); err != nil || dup {
		t.Fatalf("an expired event id is new again: dup=%v err=%v", dup, err)
	}
	if n := f.receiptCount(t, f.storeA); n != 2 {
		t.Fatalf("store A rows = %d, want the new receipt and body only", n)
	}
	if n := f.receiptCount(t, f.storeB); n != 1 {
		t.Fatalf("store B rows = %d: another store's purge touched it", n)
	}
}
