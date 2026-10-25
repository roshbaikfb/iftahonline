package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
)

// TestTrack: a shopper finds an order by its code and the phone it was
// placed with, typed any usual way; anything else is the same "not
// found", so the page tells nothing about which part was wrong.
func TestTrack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	p, err := f.svc.PlaceOrder(f.shopperA, newOrder("lookup-a", domain.LineInput{VariantID: tee, Qty: 2}))
	if err != nil {
		t.Fatal(err)
	}
	code := domain.CodeOf(p.Order.ID)
	other := newOrder("lookup-b", domain.LineInput{VariantID: tee, Qty: 1})
	other.Phone = "01099998888"
	if _, err = f.svc.PlaceOrder(f.shopperA, other); err != nil {
		t.Fatal(err)
	}

	for _, typed := range []struct{ code, phone string }{
		{code, "01012345678"},
		{"#" + strings.ToLower(code), "010 1234 5678"},
		{code[:4] + " " + code[4:], "+201012345678"},
	} {
		got, err := f.svc.Track(f.shopperA, typed.code, typed.phone)
		if err != nil || got.ID != p.Order.ID || got.Status != domain.StatusPending || got.ItemCount != 2 {
			t.Fatalf("track %+v: %+v %v", typed, got, err)
		}
	}
	for name, typed := range map[string]struct{ code, phone string }{
		"another phone":       {code, "01099998888"},
		"another code":        {"ABCDEFGH", "01012345678"},
		"not a code":          {"!!", "01012345678"},
		"not a phone":         {code, "12"},
		"nothing":             {"", ""},
		"another store's ctx": {code, "01012345678"},
	} {
		ctx := f.shopperA
		if name == "another store's ctx" {
			ctx = f.ctxB
		}
		if _, err := f.svc.Track(ctx, typed.code, typed.phone); !errors.Is(err, app.ErrOrderNotFound) {
			t.Errorf("%s: %v, want not found", name, err)
		}
	}
}
