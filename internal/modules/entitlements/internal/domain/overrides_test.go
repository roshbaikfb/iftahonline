package domain_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestOverrideValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		o      domain.Override
		fields []string
	}{
		{"flag on", domain.Override{Feature: domain.Coupons, Grant: domain.Grant{Value: 1}, Reason: "promo month"}, nil},
		{"unlimited count until a date", domain.Override{Feature: domain.Products, Grant: domain.Grant{Unlimited: true}, ExpiresAt: now.Add(time.Hour), Reason: "migration"}, nil},
		{"display-only key", domain.Override{Feature: "live_chat", Grant: domain.Grant{Value: 1}, Reason: "promo"}, []string{"feature"}},
		{"flag of 2", domain.Override{Feature: domain.Pixels, Grant: domain.Grant{Value: 2}, Reason: "promo"}, []string{"value"}},
		{"unlimited flag", domain.Override{Feature: domain.Pixels, Grant: domain.Grant{Unlimited: true}, Reason: "promo"}, []string{"value"}},
		{"negative", domain.Override{Feature: domain.Staff, Grant: domain.Grant{Value: -1}, Reason: "promo"}, []string{"value"}},
		{"past deadline, no reason", domain.Override{Feature: domain.Staff, Grant: domain.Grant{Value: 3}, ExpiresAt: now}, []string{"expires_at", "reason"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			if e, ok := errors.AsType[*errs.Error](tt.o.Validate(now)); ok {
				for _, f := range e.Fields {
					got = append(got, f.Field)
				}
			}
			if !slices.Equal(got, tt.fields) {
				t.Fatalf("fields = %v, want %v", got, tt.fields)
			}
		})
	}
}

func TestGrantsWith(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	plan := domain.Grants{domain.Coupons: {Value: 0}, domain.Products: {Value: 50}, domain.Staff: {Value: 1}}
	got := plan.With([]domain.Override{
		{Feature: domain.Coupons, Grant: domain.Grant{Value: 1}},                                        // on
		{Feature: domain.Products, Grant: domain.Grant{Unlimited: true}, ExpiresAt: now.Add(time.Hour)}, // until later
		{Feature: domain.Staff, Grant: domain.Grant{Value: 9}, ExpiresAt: now},                          // expired
		{Feature: domain.Pixels, Grant: domain.Grant{Value: 1}},                                         // not in the plan
		{Feature: "live_chat", Grant: domain.Grant{Value: 1}},                                           // not enforced
	}, now)
	want := domain.Grants{domain.Coupons: {Value: 1}, domain.Products: {Unlimited: true}, domain.Staff: {Value: 1}, domain.Pixels: {Value: 1}}
	if len(got) != len(want) {
		t.Fatalf("grants = %v", got)
	}
	for f, g := range want {
		if got[f] != g {
			t.Fatalf("%s = %+v, want %+v (all %v)", f, got[f], g, got)
		}
	}
	if plan[domain.Coupons].Value != 0 {
		t.Fatal("the plan's grants were changed")
	}
}
