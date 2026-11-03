package app_test

import (
	"slices"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestHealth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	f.createStore(t)

	h, err := f.svc.Health(ctx)
	if err != nil || !h.Database || !h.Redis || len(h.Errors) != 0 || h.Stores == nil || h.Stores.Total < 1 || h.Stores.Active < 1 ||
		h.Jobs == nil || h.Billing == nil || h.CheckedAt.IsZero() {
		t.Fatalf("health = %+v, %v", h, err)
	}
	if _, err = f.svc.Health(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	// Without Redis and billing, the page still answers and names them.
	bare := f.withoutBilling()
	h, err = bare.Health(ctx)
	if err != nil || !h.Database || h.Redis || h.Billing != nil || !slices.Equal(h.Errors, []string{"redis", "billing"}) {
		t.Fatalf("degraded health = %+v, %v", h, err)
	}
}

// TestMetrics: the console's revenue and new stores (ADR-0075), and the
// page still answering without billing.
func TestMetrics(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	f.createStore(t)

	m, err := f.svc.Metrics(ctx)
	if err != nil || len(m.Errors) != 0 || m.Revenue == nil || m.NewStores == nil || m.NewStores.Last30 < 1 || len(m.NewStores.Daily) != 30 || m.ComputedAt.IsZero() {
		t.Fatalf("metrics = %+v, %v", m, err)
	}
	if _, err = f.svc.Metrics(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	bare := f.withoutBilling()
	m, err = bare.Metrics(ctx)
	if err != nil || m.Revenue != nil || m.NewStores == nil || !slices.Equal(m.Errors, []string{"revenue"}) {
		t.Fatalf("without billing = %+v, %v", m, err)
	}
}

func TestStoreBillingAndSettle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	shop := f.createStore(t)

	s, err := f.svc.StoreBilling(ctx, shop)
	if err != nil || s.PlanNameEn == "" || s.Status != "active" || len(s.OpenInvoices) != 0 {
		t.Fatalf("billing = %+v, %v", s, err)
	}
	if _, err = f.svc.StoreBilling(ctx, tenant.StoreID{ID: f.ids.New()}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown shop: %v", err)
	}

	invoice := f.ids.New()
	if _, err = f.svc.SettleInvoice(ctx, shop, invoice, "TRX-1", "bank transfer received"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown invoice: %v", err)
	}
	for name, err := range map[string]error{
		"no reference": func() error { _, e := f.svc.SettleInvoice(ctx, shop, invoice, " ", "bank transfer"); return e }(),
		"no reason":    func() error { _, e := f.svc.SettleInvoice(ctx, shop, invoice, "TRX-1", ""); return e }(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The refused attempt is in the admin log.
	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Items, func(e app.AuditItem) bool {
		return e.AdminID != nil && *e.AdminID == me.Admin.ID && e.Action == "invoice.settled" && e.TargetID == invoice.String()
	}) {
		t.Fatal("settle attempt not in the admin log")
	}
	if _, err = f.cli.StoreBilling(ctx, shop); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}

// TestDuplicatePaymentsFromTheConsole: the console lists duplicate
// payments and records a refund with its reference, the reason first in
// the admin log (ADR-0063). Billing's own tests cover a real duplicate.
func TestDuplicatePaymentsFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	if _, err := f.svc.DuplicatePayments(ctx); err != nil {
		t.Fatal(err)
	}
	unknown := f.ids.New()
	if err := f.svc.MarkRefunded(ctx, unknown, "PMB-1", "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if err := f.svc.MarkRefunded(ctx, unknown, " ", "refunded on Paymob"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reference: %v", err)
	}
	if err := f.svc.MarkRefunded(ctx, unknown, "PMB-1", "refunded on Paymob"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown payment: %v", err)
	}
	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Items {
		found = found || e.AdminID != nil && *e.AdminID == me.Admin.ID && e.Action == "payment.refunded" && e.TargetID == unknown.String()
	}
	if !found {
		t.Fatal("the refund was not recorded first")
	}
	if _, err = f.svc.DuplicatePayments(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if err = f.svc.MarkRefunded(t.Context(), unknown, "PMB-1", "refunded"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.cli.DuplicatePayments(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
	if err = f.cli.MarkRefunded(ctx, unknown, "PMB-1", "refunded"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}

// TestAuditFilters: the log narrows by store (its actions and its
// invoices'), family, admin and Cairo days, and names each entry's admin
// and store.
func TestAuditFilters(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	one, me := f.signedIn(t)
	two, other := f.signedIn(t)
	a, b := f.createStore(t), f.createStore(t)

	for _, step := range []error{
		func() error { _, e := f.svc.Suspend(one, a, "fraud report"); return e }(),
		func() error { _, e := f.svc.Reactivate(one, a, "report cleared"); return e }(),
		func() error { _, e := f.svc.Suspend(two, b, "spam store"); return e }(),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	// A refused settlement is in the log too, naming the store.
	if _, err := f.svc.SettleInvoice(one, a, f.ids.New(), "TRX-1", "bank transfer"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("settle: %v", err)
	}

	list := func(q app.AuditQuery) []app.AuditItem {
		t.Helper()
		p, err := f.svc.Audit(one, q, "", 200)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		return p.Items
	}
	actions := func(items []app.AuditItem) []string {
		out := make([]string, len(items))
		for i, e := range items {
			out[i] = e.Action
		}
		return out
	}
	storeA := a.ID
	today := time.Now().In(cairoZone(t)).Format(time.DateOnly)
	tomorrow := time.Now().In(cairoZone(t)).AddDate(0, 0, 1).Format(time.DateOnly)
	yesterday := time.Now().In(cairoZone(t)).AddDate(0, 0, -1).Format(time.DateOnly)

	got := list(app.AuditQuery{Store: &storeA})
	if want := []string{"invoice.settled", "store.reactivated", "store.suspended"}; !slices.Equal(actions(got), want) {
		t.Fatalf("store A = %v; want %v", actions(got), want)
	}
	for _, e := range got {
		if e.StoreName != "Victim" || e.AdminName != "Ops" || e.AdminID == nil || *e.AdminID != me.Admin.ID {
			t.Errorf("%s names: store %q, admin %q %v", e.Action, e.StoreName, e.AdminName, e.AdminID)
		}
	}
	for name, tt := range map[string]struct {
		q    app.AuditQuery
		want []string
	}{
		"its store actions":   {app.AuditQuery{Store: &storeA, Family: "store"}, []string{"store.reactivated", "store.suspended"}},
		"its billing actions": {app.AuditQuery{Store: &storeA, Family: "billing"}, []string{"invoice.settled"}},
		"today, in Cairo":     {app.AuditQuery{Store: &storeA, From: today, To: today}, []string{"invoice.settled", "store.reactivated", "store.suspended"}},
		"from tomorrow":       {app.AuditQuery{Store: &storeA, From: tomorrow}, []string{}},
		"until yesterday":     {app.AuditQuery{Store: &storeA, To: yesterday}, []string{}},
		"the other admin's":   {app.AuditQuery{Admin: &other.Admin.ID, Family: "store"}, []string{"store.suspended"}},
		"their sign-ins":      {app.AuditQuery{Admin: &other.Admin.ID, Family: "security"}, []string{"admin.signed_in"}},
	} {
		if got := actions(list(tt.q)); !slices.Equal(got, tt.want) {
			t.Errorf("%s = %v; want %v", name, got, tt.want)
		}
	}
	for name, q := range map[string]app.AuditQuery{
		"an unknown family": {Family: "money"},
		"a bad date":        {From: "03/11/2026"},
		"a range backwards": {From: today, To: yesterday},
	} {
		if _, err := f.svc.Audit(one, q, "", 10); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}

	admins, err := f.svc.Admins(one)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ulid.ID{me.Admin.ID, other.Admin.ID} {
		if !slices.ContainsFunc(admins, func(x store.AdminSummary) bool { return x.ID == id && x.Name == "Ops" && x.Active }) {
			t.Errorf("admin %s not listed", id)
		}
	}
	if _, err = f.svc.Admins(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}

func cairoZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
