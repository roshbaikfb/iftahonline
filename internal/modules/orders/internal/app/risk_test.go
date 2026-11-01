package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// riskRules is a RiskCheck that gives every phone the same verdict and
// counts its calls.
type riskRules struct {
	mu      sync.Mutex
	verdict domain.Risk
	err     error
	calls   int
}

func (r *riskRules) Check(context.Context, phone.E164) (domain.Risk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.verdict, r.err
}

// TestRiskRules (ADR-0066): a held order cannot be confirmed until
// released; a verified one cannot be confirmed by the customer; only
// storefront cash orders are checked.
func TestRiskRules(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rules := &riskRules{verdict: domain.Risk{Band: "high", Action: domain.RiskHold}}
	f.env.Risk = rules
	svc := f.env.Service(f.pool)
	v := f.product(t, f.storeA, "ساعة", 120000, 20)
	place := func(qty int, mod func(*domain.NewOrder)) domain.Order {
		t.Helper()
		in := newOrder("", domain.LineInput{VariantID: v, Qty: qty})
		if mod != nil {
			mod(&in)
		}
		p, err := svc.PlaceOrder(f.shopperA, in)
		if err != nil {
			t.Fatal(err)
		}
		return p.Order
	}

	held := place(1, nil)
	if held.RiskAction != domain.RiskHold || held.RiskBand != "high" {
		t.Fatalf("placed: %q %q", held.RiskAction, held.RiskBand)
	}
	if read, _ := svc.Get(f.ctxA, held.ID); read.RiskAction != domain.RiskHold || read.RiskBand != "high" {
		t.Fatalf("stored: %q %q", read.RiskAction, read.RiskBand)
	}
	if _, err := svc.Transition(f.ctxA, held.ID, domain.StatusConfirmed, "", 0); !errors.Is(err, domain.ErrOrderHeld) || !errs.Is(err, errs.KindConflict) {
		t.Fatalf("confirming a held order: %v", err)
	}
	// Cancelling needs no release.
	if _, err := svc.ReleaseHold(f.asRole(f.shopperA, authz.RoleShipping), held.ID); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("release without orders.manage: %v", err)
	}
	if _, err := svc.ReleaseHold(f.ctxB, held.ID); err == nil {
		t.Fatal("store B released A's order")
	}
	f.audit.Take()
	released, err := svc.ReleaseHold(f.ctxA, held.ID)
	if err != nil || released.RiskAction != domain.RiskReleased || released.Version != held.Version+1 {
		t.Fatalf("release: %+v, %v", released, err)
	}
	if got := f.audit.Take(); len(got) != 1 || !strings.HasPrefix(got[0], "order.released ") {
		t.Fatalf("audit = %v", got)
	}
	if _, err = svc.ReleaseHold(f.ctxA, held.ID); !errors.Is(err, domain.ErrNotHeld) {
		t.Fatalf("releasing twice: %v", err)
	}
	if o, mErr := svc.Transition(f.ctxA, held.ID, domain.StatusConfirmed, "", 0); mErr != nil || o.Status != domain.StatusConfirmed {
		t.Fatalf("confirming after the release: %v", mErr)
	}

	// Verify: the customer's own yes is refused; the merchant's is not.
	rules.verdict = domain.Risk{Band: "medium", Action: domain.RiskVerify}
	ver := place(2, nil)
	byCustomer := func(id domain.Order) error {
		return postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
			_, mErr := svc.TransitionSystemTx(f.shopperA, tx, id.ID, domain.StatusConfirmed, "customer:whatsapp", "")
			return mErr
		})
	}
	if err = byCustomer(ver); !errors.Is(err, domain.ErrNeedsCall) {
		t.Fatalf("the customer confirming a verified order: %v", err)
	}
	if _, err = svc.Transition(f.ctxA, ver.ID, domain.StatusConfirmed, "", 0); err != nil {
		t.Fatalf("the merchant confirming it: %v", err)
	}
	if _, err = svc.ReleaseHold(f.ctxA, ver.ID); !errors.Is(err, domain.ErrNotHeld) {
		t.Fatalf("releasing a verified order: %v", err)
	}

	// Staff orders and card orders are not checked.
	before := rules.calls
	staff := place(3, func(in *domain.NewOrder) { in.Source = domain.SourceMerchant })
	card := place(4, func(in *domain.NewOrder) { in.PaymentMethod = domain.PaymentCard })
	if rules.calls != before || staff.RiskAction != domain.RiskNone || card.RiskAction != domain.RiskNone {
		t.Fatalf("checked %d more; staff %q, card %q", rules.calls-before, staff.RiskAction, card.RiskAction)
	}

	// A rule that cannot be read refuses the order rather than skip it.
	rules.err = errors.New("boom")
	if _, err = svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: 5})); err == nil {
		t.Fatal("a failed check must fail the order")
	}
}
