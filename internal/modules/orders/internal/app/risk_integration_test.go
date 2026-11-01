package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// realRules is the risk module's checker as orders see it (what
// cmd/platform's riskRules adapter does).
type realRules struct{ c risk.Checker }

func (r realRules) Check(ctx context.Context, p phone.E164) (domain.Risk, error) {
	v, err := r.c.Check(ctx, p)
	out := domain.Risk{Band: string(v.Band)}
	switch v.Action {
	case risk.Hold:
		out.Action = domain.RiskHold
	case risk.Verify:
		out.Action = domain.RiskVerify
	case risk.Prepay:
		out.Action = domain.RiskPrepay
	case risk.NoAction:
	}
	return out, err
}

// TestRiskRulesEndToEnd (P8 review): the real risk module and the real
// orders. Refusals in two stores make a phone high; the store's rules
// then hold its next order until the merchant releases it, or have the
// merchant verify it, the customer's own yes not confirming it.
func TestRiskRulesEndToEnd(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	key := []byte(strings.Repeat("r", 32))
	learner, err := risk.New(risk.Deps{Pool: f.pool, Orders: f.svc, PhoneKey: key, Jobs: testkit.NewJobs()})
	if err != nil {
		t.Fatal(err)
	}
	checker, err := risk.NewChecker(risk.CheckerDeps{Pool: f.pool, PhoneKey: key})
	if err != nil {
		t.Fatal(err)
	}
	learn := func(ctx context.Context, id ulid.ID) {
		t.Helper()
		payload, mErr := json.Marshal(map[string]string{"order_id": id.String()})
		if mErr != nil {
			t.Fatal(mErr)
		}
		if hErr := learner.LearnHandler()(ctx, eventbus.Delivery{Payload: payload}); hErr != nil {
			t.Fatal(hErr)
		}
	}
	va, vb := f.product(t, f.storeA, "ساعة", 30000, 20), f.product(t, f.storeB, "ساعة", 30000, 20)
	// A refusal in each store: the merchant cancels, saying why.
	for _, s := range []struct {
		owner context.Context
		v     ulid.ID
	}{{f.ctxA, va}, {f.ctxB, vb}} {
		p, pErr := f.svc.PlaceOrder(s.owner, merchantOrder(domain.LineInput{VariantID: s.v, Qty: 1}))
		if pErr != nil {
			t.Fatal(pErr)
		}
		if _, pErr = f.svc.TransitionWithReason(s.owner, p.Order.ID, domain.StatusCancelled, "", "refused", 0); pErr != nil {
			t.Fatal(pErr)
		}
		learn(s.owner, p.Order.ID)
	}
	rules := func(hold, verify string) {
		t.Helper()
		if rErr := postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
			_, eErr := tx.Exec(f.shopperA, `INSERT INTO risk_settings (store_id, hold_from, verify_from, prepay_from) VALUES ($1, $2, $3, 'off')
				ON CONFLICT (store_id) DO UPDATE SET hold_from = excluded.hold_from, verify_from = excluded.verify_from`, f.storeA.ID, hold, verify)
			return eErr
		}); rErr != nil {
			t.Fatal(rErr)
		}
	}
	f.env.Risk = realRules{checker}
	svc := f.env.Service(f.pool)
	place := func(qty int) domain.Order {
		t.Helper()
		p, pErr := svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: va, Qty: qty}))
		if pErr != nil {
			t.Fatal(pErr)
		}
		return p.Order
	}

	rules("high", "off")
	held := place(2)
	if held.RiskAction != domain.RiskHold || held.RiskBand != "high" {
		t.Fatalf("placed: %s %s, want held as high", held.RiskAction, held.RiskBand)
	}
	if _, err = svc.Transition(f.ctxA, held.ID, domain.StatusConfirmed, "", 0); !errors.Is(err, domain.ErrOrderHeld) {
		t.Fatalf("confirming a held order: %v", err)
	}
	if _, err = svc.ReleaseHold(f.ctxA, held.ID); err != nil {
		t.Fatal(err)
	}
	if o, tErr := svc.Transition(f.ctxA, held.ID, domain.StatusConfirmed, "", 0); tErr != nil || o.Status != domain.StatusConfirmed {
		t.Fatalf("confirming after the release: %v", tErr)
	}

	rules("off", "medium")
	ver := place(3)
	if ver.RiskAction != domain.RiskVerify {
		t.Fatalf("placed: %s, want verify", ver.RiskAction)
	}
	err = postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
		_, tErr := svc.TransitionSystemTx(f.shopperA, tx, ver.ID, domain.StatusConfirmed, "customer:whatsapp", "")
		return tErr
	})
	if !errors.Is(err, domain.ErrNeedsCall) {
		t.Fatalf("the customer's own yes: %v", err)
	}
	if o, tErr := svc.Transition(f.ctxA, ver.ID, domain.StatusConfirmed, "", 0); tErr != nil || o.Status != domain.StatusConfirmed {
		t.Fatalf("the merchant's yes after the call: %v", tErr)
	}

	// A new customer is neither held nor verified.
	rules("high", "medium")
	other := newOrder("", domain.LineInput{VariantID: va, Qty: 4})
	other.Phone = "01099990001"
	p, err := svc.PlaceOrder(f.shopperA, other)
	if err != nil || p.Order.RiskAction != domain.RiskNone || p.Order.RiskBand != "new" {
		t.Fatalf("a new customer: %+v, %v", p.Order, err)
	}
}
