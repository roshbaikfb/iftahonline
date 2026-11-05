package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestSwitchToPays is the approved offer (ADR-0087): an unpaid store
// moves to pay as you sell during its grace, its renewal invoice voided,
// active and open again, paying its fees the way it chose; then it may
// change the way. A held store may not switch. Not parallel: it turns
// the platform's collection settings on, and parallel tests wait for it.
func TestSwitchToPays(t *testing.T) {
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})
	if err = console.SaveCollectionSettings(t.Context(), domain.CollectionSettings{Prepaid: true, Card: true, Minimums: approvedMinimums}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rErr := console.SaveCollectionSettings(context.Background(), domain.CollectionSettings{Minimums: approvedMinimums}); rErr != nil {
			t.Error(rErr)
		}
	})

	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if err = d.svc.SavePaymentMethod(t.Context(), account, "fake", "decline-card", "Visa •••• 2346"); err != nil {
		t.Fatal(err)
	}
	if n := d.tick(t, runAt.Add(time.Hour)); len(n) != 1 {
		t.Fatalf("day 0 notices = %v", n)
	}

	// Past due: the offer is there, with the fee and the ways offered.
	sum, err := d.svc.Summary(ctx)
	if err != nil || sum.Status != domain.PastDue || sum.Switch == nil || sum.Switch.RateBps <= 0 || len(sum.Switch.Offered) != 2 || sum.Agency {
		t.Fatalf("offer = %+v, %v", sum, err)
	}
	if err = d.svc.SwitchToPays(ctx, "cash"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an unknown way: %v", err)
	}
	if err = d.svc.SwitchToPays(ctx, domain.CollectBalance); err != nil {
		t.Fatal(err)
	}
	st, grace, _, billed := d.state(ctx, t)
	if st != "active" || grace != nil || billed || d.holds.of(ctx) {
		t.Fatalf("after the switch: %s, grace %v, renewal billed %v", st, grace, billed)
	}
	if s, _, _ := d.invoice(t, account); s != "void" {
		t.Fatalf("the renewal invoice is %s", s)
	}
	if sum, err = d.svc.Summary(ctx); err != nil || sum.Switch != nil || sum.Collection != domain.CollectBalance || len(sum.OpenInvoices) != 0 {
		t.Fatalf("after = %+v, %v", sum, err)
	}
	// Nothing is charged any more, and the store cannot switch twice.
	before := len(d.gw.charges)
	if d.tick(t, runAt.Add(48*time.Hour)); len(d.gw.charges) != before {
		t.Fatal("a voided invoice was charged")
	}
	if err = d.svc.SwitchToPays(ctx, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("switch twice: %v", err)
	}

	// It changes the way: to the card it saved.
	if err = d.svc.SetCollection(ctx, domain.CollectCard); err != nil {
		t.Fatal(err)
	}
	if sum, _ = d.svc.Summary(ctx); sum.Collection != domain.CollectCard {
		t.Fatalf("collection = %q", sum.Collection)
	}

	// A held store may not switch; a store not on pay as you sell may not
	// choose a way; the card needs one saved.
	held := d.store(t)
	d.subscribe(held, t, "pro", "suspended",
		"cycle, suspended_at, held_at='monthly', '2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z'")
	if err = d.svc.SwitchToPays(held, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("held: %v", err)
	}
	other := d.store(t)
	d.subscribe(other, t, "pro", "active", "")
	if err = d.svc.SetCollection(other, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("not on pays: %v", err)
	}
	pays := d.store(t)
	d.subscribe(pays, t, "pays", "active", "")
	if err = d.svc.SetCollection(pays, domain.CollectCard); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("no card: %v", err)
	}
	if err = d.svc.SetCollection(pays, domain.CollectBalance); err != nil {
		t.Fatal(err)
	}
}

// TestLeavingTheBalanceWaitsForItsInvoice: fees taken from the balance
// keep the account on it until the month's invoice takes their tax.
func TestLeavingTheBalanceWaitsForItsInvoice(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	credit, _ := domain.ParseCredit(30000, "transfer", "CIB 4")
	if _, err := p.svc.CreditBalance(ctx, credit, p.ids.New()); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)
	p.choose(ctx, t, account)
	if err := p.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Visa •••• 4242"); err != nil {
		t.Fatal(err)
	}
	p.move(ctx, t, p.ids.New(), app.OrderDelivered, 100000, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if code := codeOf(p.svc.SetCollection(ctx, domain.CollectCard)); code != "prepaid_pending" {
		t.Fatalf("with prepaid fees pending: %q", code)
	}
	if err := p.svc.InvoiceAccount(t.Context(), account, october); err != nil {
		t.Fatal(err)
	}
	// Invoiced: the fees no longer hold it; the card is then refused only
	// because the platform does not offer it (the defaults).
	if code := codeOf(p.svc.SetCollection(ctx, domain.CollectCard)); code != "collection_not_offered" {
		t.Fatalf("after the invoice: %q", code)
	}
}

func codeOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestAgencyStoreIsSentToSupport: an agency's store gets no offer to
// switch, and the platform's WhatsApp number to arrange it; other stores
// do not ask for it (ADR-0087).
func TestAgencyStoreIsSentToSupport(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	asked := 0
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Holds: d.holds.set, Notifier: func(context.Context, app.Notice) error { return nil },
		Support: func(context.Context) (string, error) {
			asked++
			return "+201153450716", nil
		}})
	agency := d.store(t)
	d.subscribe(agency, t, "agency", "past_due", "grace_until='2026-11-08T00:00:00Z'")
	sum, err := d.svc.Summary(agency)
	if err != nil || !sum.Agency || sum.Switch != nil || sum.SupportWhatsApp != "+201153450716" {
		t.Fatalf("agency's store = %+v, %v", sum, err)
	}
	if err = d.svc.SwitchToPays(agency, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("an agency's store switching: %v", err)
	}
	own := d.store(t)
	d.subscribe(own, t, "pro", "past_due", "grace_until='2026-11-08T00:00:00Z'")
	if sum, err = d.svc.Summary(own); err != nil || sum.Agency || sum.SupportWhatsApp != "" || asked != 1 {
		t.Fatalf("own store = %+v, asked %d, %v", sum, asked, err)
	}
}

// TestSwitchAgencyStores: the console moves two of an unpaid agency's
// three stores to pay as you sell (ADR-0087 §7): each gets its own
// account paying by the agency's card, copied; the agency's invoice is
// voided, and the store that stays is invoiced again at the agency's
// price, still past due.
func TestSwitchAgencyStores(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	a, err := d.svc.CreateAgency(t.Context(), "وكالة مدار", "EGP", "", "agency")
	if err != nil {
		t.Fatal(err)
	}
	stores := []context.Context{d.store(t), d.store(t), d.store(t)}
	ids := make([]ulid.ID, len(stores))
	for i, sctx := range stores {
		if err = d.svc.JoinAgency(sctx, a.ID); err != nil {
			t.Fatal(err)
		}
		d.own(sctx, a.ID)
		ids[i] = ulid.ID(storeOf(sctx))
	}
	// November's renewal, declined: every store is past due.
	*d.clock = time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)
	if err = d.svc.InvoiceAccount(t.Context(), a.ID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err = d.svc.SavePaymentMethod(t.Context(), a.ID, "fake", "decline-card", "Visa •••• 1881"); err != nil {
		t.Fatal(err)
	}
	d.tick(t, d.clock.Add(time.Hour))
	before, err := d.svc.AgencyOpenInvoices(t.Context(), a.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("open invoices = %+v, %v", before, err)
	}
	for _, sctx := range stores {
		if st, _, _, _ := d.state(sctx, t); st != "past_due" {
			t.Fatalf("before: %s", st)
		}
	}

	if err = d.svc.SwitchAgencyStores(t.Context(), a.ID, []ulid.ID{ids[0], d.ids.New()}, domain.CollectCard); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a store of another account: %v", err)
	}
	if err = d.svc.SwitchAgencyStores(t.Context(), a.ID, ids[:2], domain.CollectCard); err != nil {
		t.Fatal(err)
	}
	for _, sctx := range stores[:2] {
		sum, sErr := d.svc.Summary(sctx)
		if sErr != nil || sum.Status != domain.Active || sum.Agency || sum.Collection != domain.CollectCard || sum.Card != "Visa •••• 1881" ||
			len(sum.OpenInvoices) != 0 || d.holds.of(sctx) {
			t.Fatalf("switched store = %+v, %v", sum, sErr)
		}
	}
	sum, err := d.svc.Summary(stores[2])
	if err != nil || sum.Status != domain.PastDue || !sum.Agency || len(sum.OpenInvoices) != 1 || sum.OpenInvoices[0].ID == before[0].ID ||
		sum.OpenInvoices[0].Number == before[0].Number {
		t.Fatalf("the store that stays = %+v, %v", sum, err)
	}
	if a2, members, aErr := d.svc.Agency(t.Context(), a.ID); aErr != nil || a2.Stores != 1 || len(members) != 1 || members[0] != ids[2] {
		t.Fatalf("agency = %+v %v, %v", a2, members, aErr)
	}

	// An agency that owes nothing has nothing to switch.
	paid, err := d.svc.CreateAgency(t.Context(), "وكالة النيل", "EGP", "", "agency")
	if err != nil {
		t.Fatal(err)
	}
	member := d.store(t)
	if err = d.svc.JoinAgency(member, paid.ID); err != nil {
		t.Fatal(err)
	}
	if code := codeOf(d.svc.SwitchAgencyStores(t.Context(), paid.ID, []ulid.ID{ulid.ID(storeOf(member))}, domain.CollectBalance)); code != "agency_not_unpaid" {
		t.Fatalf("an agency that owes nothing: %q", code)
	}
}

// TestSwitchAgencyStoresRefusals: the way and the stores are checked, an
// unknown agency is not found, the card needs the agency's, and every
// store may move by the balance, leaving the agency empty.
func TestSwitchAgencyStoresRefusals(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	a, err := d.svc.CreateAgency(t.Context(), "وكالة الدلتا", "EGP", "", "agency")
	if err != nil {
		t.Fatal(err)
	}
	sctx := d.store(t)
	if err = d.svc.JoinAgency(sctx, a.ID); err != nil {
		t.Fatal(err)
	}
	d.own(sctx, a.ID)
	id := ulid.ID(storeOf(sctx))
	*d.clock = time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)
	if err = d.svc.InvoiceAccount(t.Context(), a.ID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	d.tick(t, d.clock.Add(time.Hour)) // no card: past due

	for name, c := range map[string]struct {
		agency ulid.ID
		stores []ulid.ID
		way    domain.Collection
		code   string
	}{
		"an unknown way": {a.ID, []ulid.ID{id}, "cash", "validation_failed"},
		"no store":       {a.ID, nil, domain.CollectBalance, "validation_failed"},
		"unknown agency": {d.ids.New(), []ulid.ID{id}, domain.CollectBalance, "agency_not_found"},
		"no agency card": {a.ID, []ulid.ID{id}, domain.CollectCard, "card_required"},
	} {
		if code := codeOf(d.svc.SwitchAgencyStores(t.Context(), c.agency, c.stores, c.way)); code != c.code {
			t.Errorf("%s: %q, want %q", name, code, c.code)
		}
	}
	if _, err = d.svc.AgencyOpenInvoices(t.Context(), d.ids.New()); codeOf(err) != "agency_not_found" {
		t.Errorf("open invoices of an unknown agency: %v", err)
	}
	if err = d.svc.SwitchAgencyStores(t.Context(), a.ID, []ulid.ID{id}, domain.CollectBalance); err != nil {
		t.Fatal(err)
	}
	if sum, sErr := d.svc.Summary(sctx); sErr != nil || sum.Status != domain.Active || sum.Collection != domain.CollectBalance || sum.Card != "" {
		t.Fatalf("switched = %+v, %v", sum, sErr)
	}
	if open, oErr := d.svc.AgencyOpenInvoices(t.Context(), a.ID); oErr != nil || len(open) != 0 {
		t.Fatalf("an empty agency's invoices = %+v, %v", open, oErr)
	}
}
