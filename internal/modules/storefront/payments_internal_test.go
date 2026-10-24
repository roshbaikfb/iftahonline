package storefront

import (
	"context"
	"net/url"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

type cardsOn bool

func (c cardsOn) CardEnabled(context.Context) (bool, error) { return bool(c), nil }

func TestShopPayments(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	store, order := tenant.StoreID{ID: ids.New()}, ids.New()
	p := shopPayments{cards: cardsOn(true), origin: "https://app.iftahonline.com/"}
	if on, err := p.CardEnabled(t.Context()); err != nil || !on {
		t.Fatalf("enabled %v %v", on, err)
	}
	back := "https://souq.iftahonline.com/thank-you/" + order.String()
	got := p.PayURL(store.String(), order.String(), back)
	want := "https://app.iftahonline.com/v1/pay/" + store.String() + "/" + order.String() + "?return=" + url.QueryEscape(back)
	if got != want {
		t.Fatalf("PayURL = %q, want %q", got, want)
	}
	if p.PayURL("nope", order.String(), back) != "" || p.PayURL(store.String(), "IFT-1", back) != "" {
		t.Fatal("a pay URL for a malformed store or order")
	}
	if method(true) != orders.PaymentCard || method(false) != orders.PaymentCOD {
		t.Fatal("payment methods")
	}
}
