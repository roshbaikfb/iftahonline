package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestCardsNeedThePlan: a plan without card payments connects no gateway
// and sends no shopper to one, even with a gateway already connected (a
// lapsed trial, a downgrade) (ADR-0046).
func TestCardsNeedThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{}})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Gateways: app.Gateways{fake.Name: f.gw}, Sealer: f.sealer(),
		PublicURL: "https://app.example.com", Orders: f.orders, Jobs: f.jobs, Inbox: f.inbox, Plan: ent.Entitlements()})

	_, err = svc.Connect(f.ctxB, app.ConnectInput{Gateway: fake.Name, APIKey: apiKey, WebhookSecret: hookSecret})
	if !errors.Is(err, entitlements.ErrLimitReached) || errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("connect without the plan: %v", err)
	}
	if on, cErr := svc.CardEnabled(f.ctxA); cErr != nil || on {
		t.Fatalf("A is connected but its plan has no cards: %v %v", on, cErr)
	}
	order := f.orders.Add(t, orders.PaymentCard, 0)
	if _, err = svc.StartCheckout(f.ctxA, order, "https://shop.example.com/thank-you/x"); !errors.Is(err, app.ErrCardUnavailable) {
		t.Fatalf("checkout without the plan: %v", err)
	}
	if n := len(f.jobs.Kinds()); n != 0 {
		t.Fatalf("%d jobs, want none", n)
	}
}
