package domain_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestFoundingRenewal(t *testing.T) {
	t.Parallel()
	m := domain.Member{DiscountBps: 2000, Currency: "EGP", PlanCode: "merchant", Cycle: domain.Monthly, LockedMinor: 29900}
	for _, tt := range []struct {
		name              string
		code              string
		cycle             domain.Cycle
		currency          string
		current           int64
		charged, discount int64
		relock            bool
	}{
		{"same price", "merchant", domain.Monthly, "EGP", 29900, 29900, 5980, false},
		{"prices went up: the lock holds", "merchant", domain.Monthly, "EGP", 39900, 29900, 5980, false},
		{"prices went down: the member pays less too", "merchant", domain.Monthly, "EGP", 24900, 24900, 4980, false},
		{"an upgrade: the new plan's price, locked", "pro", domain.Monthly, "EGP", 59900, 59900, 11980, true},
		{"annual: the annual price, locked", "merchant", domain.Annual, "EGP", 299000, 299000, 59800, true},
		{"another currency", "merchant", domain.Monthly, "SAR", 4900, 4900, 980, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			charged, discount, relock := m.Renewal(tt.code, tt.cycle, tt.currency, tt.current)
			if charged != tt.charged || discount != tt.discount || relock != tt.relock {
				t.Fatalf("= %d, %d, %v; want %d, %d, %v", charged, discount, relock, tt.charged, tt.discount, tt.relock)
			}
		})
	}
}

func TestDiscount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		amount int64
		bps    int32
		want   int64
	}{{29900, 2000, 5980}, {12345, 2000, 2469}, {1, 2000, 0}, {3, 2000, 1}, {0, 2000, 0}, {100, 0, 0}, {-100, 2000, 0}} {
		if got := domain.Discount(tt.amount, tt.bps); got != tt.want {
			t.Errorf("Discount(%d, %d) = %d, want %d", tt.amount, tt.bps, got, tt.want)
		}
	}
	line := domain.DiscountLine(ulid.ID{1}, 5980, 2000)
	if line.Kind != domain.LineFounding || line.AmountMinor != -5980 || line.Description != "خصم التاجر المؤسس (20%)" {
		t.Fatalf("line = %+v", line)
	}
	if got := domain.Percent(2050); got != "20.50%" {
		t.Fatalf("Percent(2050) = %q", got)
	}
	// The invoice's total takes the discount off before tax.
	tot, ok := domain.Totals([]domain.Line{{Kind: domain.LineSubscription, Quantity: 1, AmountMinor: 29900}, line}, 1400)
	if !ok || tot.SubtotalMinor != 23920 || tot.TaxMinor != 3349 || tot.TotalMinor != 27269 {
		t.Fatalf("totals = %+v", tot)
	}
}

func TestProgram(t *testing.T) {
	t.Parallel()
	if err := (domain.Program{Enabled: true, Seats: 100, DiscountBps: 2000}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []domain.Program{{Seats: 0, DiscountBps: 2000}, {Seats: 100, DiscountBps: 50}, {Seats: 100, DiscountBps: 9500}, {Seats: 20000, DiscountBps: 2000}} {
		if err := bad.Validate(); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
	if (domain.Member{}).Active() != true {
		t.Fatal("a member without a loss is not active")
	}
}
