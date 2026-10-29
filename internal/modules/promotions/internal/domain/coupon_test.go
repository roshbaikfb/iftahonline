package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func intp(n int) *int { return &n }

func TestNormalizeCode(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		" summer10 ": "SUMMER10", "EID-2026": "EID-2026", "a_b": "A_B",
		"ab": "", strings.Repeat("A", 33): "", "SUMMER 10": "", "خصم10": "", "": "",
	}
	for in, want := range tests {
		got, ok := domain.NormalizeCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("NormalizeCode(%q) = %q %v", in, got, ok)
		}
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	later := now.Add(time.Hour)
	ok := []domain.Coupon{
		{Code: "TEN", Kind: domain.Percent, Percent: 10},
		{Code: "FIFTY", Kind: domain.Fixed, AmountMinor: 5000, MinSubtotalMinor: 20000, MaxUses: intp(100), MaxPerCustomer: intp(1)},
		{Code: "SHIP", Kind: domain.FreeShipping, StartsAt: &now, EndsAt: &later},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Code, err)
		}
	}
	bad := map[string]domain.Coupon{
		"code":               {Code: "x", Kind: domain.Percent, Percent: 10},
		"kind":               {Code: "ABC", Kind: "bogo"},
		"percent":            {Code: "ABC", Kind: domain.Percent, Percent: 101},
		"amount_minor":       {Code: "ABC", Kind: domain.Fixed},
		"min_subtotal_minor": {Code: "ABC", Kind: domain.FreeShipping, MinSubtotalMinor: -1},
		"max_uses":           {Code: "ABC", Kind: domain.FreeShipping, MaxUses: intp(0)},
		"max_per_customer":   {Code: "ABC", Kind: domain.FreeShipping, MaxPerCustomer: intp(-2)},
		"ends_at":            {Code: "ABC", Kind: domain.FreeShipping, StartsAt: &later, EndsAt: &now},
	}
	for field, c := range bad {
		var e *errs.Error
		if err := c.Validate(); !errors.As(err, &e) || len(e.Fields) != 1 || e.Fields[0].Field != field {
			t.Errorf("%s: %v", field, err)
		}
	}
}

func TestApply(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	base := domain.Coupon{Code: "C", Active: true}
	with := func(f func(c *domain.Coupon)) domain.Coupon { c := base; f(&c); return c }
	tests := []struct {
		name     string
		c        domain.Coupon
		subtotal int64
		uses     int
		off      int64
		free     bool
		reason   domain.Reason
	}{
		{"percent rounds down", with(func(c *domain.Coupon) { c.Kind, c.Percent = domain.Percent, 15 }), 9999, -1, 1499, false, ""},
		{"100 percent", with(func(c *domain.Coupon) { c.Kind, c.Percent = domain.Percent, 100 }), 5000, -1, 5000, false, ""},
		{"fixed", with(func(c *domain.Coupon) { c.Kind, c.AmountMinor = domain.Fixed, 3000 }), 10000, 0, 3000, false, ""},
		{"fixed at most the items", with(func(c *domain.Coupon) { c.Kind, c.AmountMinor = domain.Fixed, 30000 }), 10000, 0, 10000, false, ""},
		{"free shipping", with(func(c *domain.Coupon) { c.Kind = domain.FreeShipping }), 10000, 0, 0, true, ""},
		{"inactive", with(func(c *domain.Coupon) { c.Kind, c.Active = domain.FreeShipping, false }), 10000, 0, 0, false, domain.Inactive},
		{"not started", with(func(c *domain.Coupon) { c.Kind, c.StartsAt = domain.FreeShipping, &future }), 10000, 0, 0, false, domain.NotStarted},
		{"expired", with(func(c *domain.Coupon) { c.Kind, c.EndsAt = domain.FreeShipping, &past }), 10000, 0, 0, false, domain.Expired},
		{"ends now", with(func(c *domain.Coupon) { c.Kind, c.EndsAt = domain.FreeShipping, &now }), 10000, 0, 0, false, domain.Expired},
		{"in its window", with(func(c *domain.Coupon) { c.Kind, c.StartsAt, c.EndsAt = domain.FreeShipping, &past, &future }), 10000, 0, 0, true, ""},
		{"below minimum", with(func(c *domain.Coupon) { c.Kind, c.MinSubtotalMinor = domain.FreeShipping, 10001 }), 10000, 0, 0, false, domain.MinSubtotal},
		{"at minimum", with(func(c *domain.Coupon) { c.Kind, c.MinSubtotalMinor = domain.FreeShipping, 10000 }), 10000, 0, 0, true, ""},
		{"used up", with(func(c *domain.Coupon) { c.Kind, c.MaxUses, c.Uses = domain.FreeShipping, intp(5), 5 }), 10000, 0, 0, false, domain.UsedUp},
		{"last use", with(func(c *domain.Coupon) { c.Kind, c.MaxUses, c.Uses = domain.FreeShipping, intp(5), 4 }), 10000, 0, 0, true, ""},
		{"per customer", with(func(c *domain.Coupon) { c.Kind, c.MaxPerCustomer = domain.FreeShipping, intp(1) }), 10000, 1, 0, false, domain.PerCustomer},
		{"quote: customer unknown", with(func(c *domain.Coupon) { c.Kind, c.MaxPerCustomer = domain.FreeShipping, intp(1) }), 10000, -1, 0, true, ""},
	}
	for _, tt := range tests {
		d, err := tt.c.Apply(egp(t, tt.subtotal), now, tt.uses)
		var r *domain.Rejected
		switch {
		case tt.reason != "":
			if !errors.As(err, &r) || r.Reason != tt.reason {
				t.Errorf("%s: %v, want %s", tt.name, err, tt.reason)
			}
		case err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case d.Amount.Amount() != tt.off || d.FreeShipping != tt.free || d.Amount.Currency() != money.EGP || d.Code != "C":
			t.Errorf("%s: %+v", tt.name, d)
		}
	}
	var r *domain.Rejected
	_, err := with(func(c *domain.Coupon) { c.Kind, c.MinSubtotalMinor = domain.Fixed, 50000 }).Apply(egp(t, 100), now, 0)
	if !errors.As(err, &r) || r.MinSubtotalMinor != 50000 || !strings.Contains(err.Error(), "min_subtotal") {
		t.Fatalf("minimum carried: %v", err)
	}
	if domain.Kind("x").Valid() || !domain.Percent.Valid() {
		t.Fatal("kinds")
	}
}
