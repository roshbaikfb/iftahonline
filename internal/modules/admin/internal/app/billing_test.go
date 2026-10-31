package app_test

import (
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
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
	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Items, func(e store.AuditEntry) bool {
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
	page, err := f.svc.Audit(ctx, "", 50)
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
