package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// clock is a settable time source.
type clock struct{ t atomic.Pointer[time.Time] }

func newClock(t time.Time) *clock {
	c := &clock{}
	c.set(t)
	return c
}
func (c *clock) set(t time.Time)         { c.t.Store(&t) }
func (c *clock) now() time.Time          { return *c.t.Load() }
func (c *clock) advance(d time.Duration) { c.set(c.now().Add(d)) }

type fixture struct {
	svc   *app.Service
	pool  *pgxpool.Pool
	clock *clock
	ids   *ulid.Generator
}

func newFixture(t *testing.T, limits app.Limits) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := fixture{pool: pool, clock: newClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)), ids: ulid.NewGenerator()}
	f.svc = app.NewService(app.Deps{DB: pool, Limits: limits, Now: f.clock.now})
	return f
}

func (f fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "ent-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (f fixture) consume(ctx context.Context, n int64) (u domain.Usage, err error) {
	err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		u, txErr = f.svc.ConsumeTx(ctx, tx, domain.Messages, n)
		return txErr
	})
	return u, err
}

func (f fixture) release(ctx context.Context, period time.Time, n int64) (u domain.Usage, err error) {
	err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (txErr error) {
		u, txErr = f.svc.ReleaseTx(ctx, tx, domain.Messages, period, n)
		return txErr
	})
	return u, err
}

func TestConsumeUpToTheLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 5})
	ctx := f.store(t)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	var crossed []domain.Threshold
	for i := int64(1); i <= 5; i++ {
		u, err := f.consume(ctx, 1)
		if err != nil || u.Used != i || u.Limit != 5 || !u.Period.Equal(october) {
			t.Fatalf("consume %d = %+v, %v", i, u, err)
		}
		if th := u.CrossedBy(1); th != domain.NoThreshold {
			crossed = append(crossed, th)
		}
	}
	if len(crossed) != 2 || crossed[0] != domain.Nearly || crossed[1] != domain.Reached {
		t.Fatalf("warnings = %v, want [80 100]", crossed)
	}

	// Past the limit: nothing counted, the usage as it is.
	u, err := f.consume(ctx, 1)
	if !errors.Is(err, domain.ErrQuotaExceeded) || u.Used != 5 || u.Remaining() != 0 {
		t.Fatalf("over the limit = %+v, %v", u, err)
	}
	if u, err = f.svc.Usage(ctx, domain.Messages); err != nil || u.Used != 5 || u.Limit != 5 {
		t.Fatalf("Usage = %+v, %v", u, err)
	}

	// Giving units back makes room again.
	if u, err = f.release(ctx, october, 2); err != nil || u.Used != 3 {
		t.Fatalf("release = %+v, %v", u, err)
	}
	if u, err = f.consume(ctx, 2); err != nil || u.Used != 5 {
		t.Fatalf("consume after release = %+v, %v", u, err)
	}
	// A request bigger than what is left counts nothing.
	if _, err = f.release(ctx, october, 1); err != nil {
		t.Fatal(err)
	}
	if u, err = f.consume(ctx, 2); !errors.Is(err, domain.ErrQuotaExceeded) || u.Used != 4 {
		t.Fatalf("2 with 1 left = %+v, %v", u, err)
	}
}

func TestNewMonthStartsAtZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 2})
	ctx := f.store(t)
	if _, err := f.consume(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.consume(ctx, 1); !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("October over = %v", err)
	}
	f.clock.advance(4 * 24 * time.Hour) // November 1st
	u, err := f.consume(ctx, 1)
	if err != nil || u.Used != 1 || !u.Period.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("November = %+v, %v", u, err)
	}
	// A late release of an October unit leaves November alone.
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if u, err = f.release(ctx, october, 1); err != nil || u.Used != 1 || !u.Period.Equal(october) {
		t.Fatalf("October release = %+v, %v", u, err)
	}
	if u, err = f.svc.Usage(ctx, domain.Messages); err != nil || u.Used != 1 {
		t.Fatalf("November usage = %+v, %v", u, err)
	}
}

func TestRolledBackConsumptionDoesNotCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 10})
	ctx := f.store(t)
	boom := errors.New("send failed")
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		if _, err := f.svc.ConsumeTx(ctx, tx, domain.Messages, 3); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if u, err := f.svc.Usage(ctx, domain.Messages); err != nil || u.Used != 0 {
		t.Fatalf("usage after rollback = %+v, %v", u, err)
	}
}

// TestLastUnitIsSpentOnce: concurrent senders of one store race for the
// last units; exactly the limit succeeds.
func TestLastUnitIsSpentOnce(t *testing.T) {
	t.Parallel()
	const limit, senders = 5, 20
	f := newFixture(t, app.StaticLimits{domain.Messages: limit})
	ctx := f.store(t)
	var ok, over atomic.Int64
	var wg sync.WaitGroup
	errc := make(chan error, senders)
	for range senders {
		wg.Go(func() {
			_, err := f.consume(ctx, 1)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrQuotaExceeded):
				over.Add(1)
			default:
				errc <- err
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if ok.Load() != limit || over.Load() != senders-limit {
		t.Fatalf("ok = %d, over = %d", ok.Load(), over.Load())
	}
	if u, err := f.svc.Usage(ctx, domain.Messages); err != nil || u.Used != limit {
		t.Fatalf("usage = %+v, %v", u, err)
	}
}

// TestStoresCountApart: one store's use is not another's, and a store
// cannot release into another's count.
func TestStoresCountApart(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 3})
	a, b := f.store(t), f.store(t)
	if _, err := f.consume(a, 3); err != nil {
		t.Fatal(err)
	}
	if u, err := f.consume(b, 1); err != nil || u.Used != 1 {
		t.Fatalf("store B = %+v, %v", u, err)
	}
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := f.release(b, october, 1); err != nil {
		t.Fatal(err)
	}
	if u, err := f.svc.Usage(a, domain.Messages); err != nil || u.Used != 3 {
		t.Fatalf("store A after B's release = %+v, %v", u, err)
	}
	if u, err := f.svc.Usage(b, domain.Messages); err != nil || u.Used != 0 {
		t.Fatalf("store B = %+v, %v", u, err)
	}
}

func TestZeroLimitSendsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{})
	ctx := f.store(t)
	u, err := f.consume(ctx, 1)
	if !errors.Is(err, domain.ErrQuotaExceeded) || u.Used != 0 || u.Limit != 0 || u.Level() != domain.Reached {
		t.Fatalf("zero limit = %+v, %v", u, err)
	}
}

func TestReleaseNeverBelowZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 10})
	ctx := f.store(t)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if u, err := f.release(ctx, october, 4); err != nil || u.Used != 0 {
		t.Fatalf("release of an uncounted month = %+v, %v", u, err)
	}
	if _, err := f.consume(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if u, err := f.release(ctx, october, 5); err != nil || u.Used != 0 {
		t.Fatalf("over-release = %+v, %v", u, err)
	}
}

type failingLimits struct{}

func (failingLimits) Limit(context.Context, domain.Feature) (int64, error) {
	return 0, errors.New("plans unavailable")
}

func TestInvalidRequests(t *testing.T) {
	t.Parallel()
	f := newFixture(t, app.StaticLimits{domain.Messages: 10})
	ctx := f.store(t)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		for _, n := range []int64{0, -1, domain.MaxUnits + 1} {
			if _, err := f.svc.ConsumeTx(ctx, tx, domain.Messages, n); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("consume %d: %v", n, err)
			}
		}
		if _, err := f.svc.ConsumeTx(ctx, tx, "sms", 1); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("unknown feature: %v", err)
		}
		if _, err := f.svc.ReleaseTx(ctx, tx, domain.Messages, october.Add(time.Hour), 1); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("mid-month period: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Usage(ctx, "sms"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("usage of unknown feature: %v", err)
	}
	if _, err = f.svc.Usage(t.Context(), domain.Messages); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("usage without a store: %v", err)
	}

	broken := app.NewService(app.Deps{DB: f.pool, Limits: failingLimits{}})
	if _, err = broken.Usage(ctx, domain.Messages); err == nil {
		t.Error("usage with failing limits: want error")
	}
	if _, err = f.release(ctx, october, 1); err != nil {
		t.Fatal(err)
	}
}
