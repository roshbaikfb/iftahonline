package app_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

var approvedMinimums = map[string]int64{"EGP": 30000, "SAR": 10000, "AED": 10000} //nolint:gochecknoglobals // a test constant

// TestCollectionSettings: both ways start off with the approved
// minimums; only the console's role may change them, within bounds
// (ADR-0083). One test: the settings are the platform's, one row.
func TestCollectionSettings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})
	t.Cleanup(func() {
		if rErr := console.SaveCollectionSettings(context.Background(), domain.CollectionSettings{Minimums: approvedMinimums}); rErr != nil {
			t.Error(rErr)
		}
	})

	got, err := f.svc.CollectionSettings(t.Context())
	if err != nil || got.Prepaid || got.Card || len(got.Minimums) != 3 || got.Minimums["EGP"] != 30000 || got.Minimums["SAR"] != 10000 ||
		got.Minimums["AED"] != 10000 {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	want := domain.CollectionSettings{Prepaid: true, Card: true, Minimums: map[string]int64{"EGP": 50000, "SAR": 15000, "AED": 15000}}
	if err = f.svc.SaveCollectionSettings(t.Context(), want); err == nil {
		t.Fatal("the merchants' role changed the settings")
	}
	if err = console.SaveCollectionSettings(t.Context(), domain.CollectionSettings{Minimums: map[string]int64{"EGP": 1}}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("out of bounds: %v", err)
	}
	if err = console.SaveCollectionSettings(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if got, err = f.svc.CollectionSettings(t.Context()); err != nil || !got.Prepaid || !got.Card || got.Minimums["EGP"] != 50000 || got.Minimums["AED"] != 15000 {
		t.Fatalf("saved = %+v, %v", got, err)
	}
}

// TestCreditBalance: the console adds to a store's balance, opening its
// account; each credit is one entry with the balance after it, made
// under the balance's lock, and another store sees none of it.
func TestCreditBalance(t *testing.T) {
	t.Parallel()
	b := newBilling(t)
	ctx := b.store(t)
	b.subscribe(ctx, t, "pays", "active", "")
	admin := b.ids.New()

	bal, err := b.svc.StoreBalance(ctx)
	if err != nil || bal.Currency != "EGP" || bal.Minor != 0 || bal.Level != domain.BalanceDebt || len(bal.Entries) != 0 || bal.MinimumMinor <= 0 {
		t.Fatalf("before = %+v, %v", bal, err)
	}
	c, err := domain.ParseCredit(50000, "transfer", "CIB 7741 2209")
	if err != nil {
		t.Fatal(err)
	}
	if bal, err = b.svc.CreditBalance(ctx, c, admin); err != nil {
		t.Fatal(err)
	}
	if bal.Minor != 50000 || bal.Level != domain.BalanceOK || bal.Collection != "" || len(bal.Entries) != 1 {
		t.Fatalf("after = %+v", bal)
	}
	e := bal.Entries[0]
	if e.Kind != domain.EntryManual || e.AmountMinor != 50000 || e.AfterMinor != 50000 || e.Currency != "EGP" || e.Reason != domain.ReasonTransfer ||
		e.Reference != "CIB 7741 2209" || e.Admin != admin || e.CreatedAt.IsZero() {
		t.Fatalf("entry = %+v", e)
	}

	// Ten credits at once: each adds to the balance as it stands.
	small, _ := domain.ParseCredit(100, "correction", "fix")
	var wg sync.WaitGroup
	errc := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			if _, cErr := b.svc.CreditBalance(ctx, small, admin); cErr != nil {
				errc <- cErr
			}
		})
	}
	wg.Wait()
	close(errc)
	for cErr := range errc {
		t.Fatal(cErr)
	}
	if bal, err = b.svc.StoreBalance(ctx); err != nil || bal.Minor != 51000 || len(bal.Entries) != 11 || bal.Entries[0].AfterMinor != 51000 {
		t.Fatalf("after ten = %d, %d entries, %v", bal.Minor, len(bal.Entries), err)
	}

	// Another store has nothing; a store with no billing row cannot hold one.
	other := b.store(t)
	b.subscribe(other, t, "pays", "active", "")
	if ob, oErr := b.svc.StoreBalance(other); oErr != nil || ob.Minor != 0 || len(ob.Entries) != 0 {
		t.Fatalf("another store = %+v, %v", ob, oErr)
	}
	if _, err = b.svc.CreditBalance(b.store(t), c, admin); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("no billing row: %v", err)
	}
	if _, err = b.svc.CreditBalance(ctx, c, [16]byte{}); err == nil {
		t.Fatal("a credit without its admin")
	}
}
