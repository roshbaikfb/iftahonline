package domain_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestMonthlyShare(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		amount int64
		cycle  domain.Cycle
		want   int64
	}{
		{29900, domain.Monthly, 29900},
		{299000, domain.Annual, 24917}, // 24916.67, rounded half up
		{120000, domain.Annual, 10000},
		{6, domain.Annual, 1}, // 0.5 rounds up
		{5, domain.Annual, 0},
		{0, domain.Annual, 0},
	} {
		if got := domain.MonthlyShare(tt.amount, tt.cycle); got != tt.want {
			t.Errorf("MonthlyShare(%d, %s) = %d; want %d", tt.amount, tt.cycle, got, tt.want)
		}
	}
}

func TestMRR(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	a, b, agency := gen.New(), gen.New(), gen.New()
	sub := func(account ulid.ID, currency, code string, cycle domain.Cycle, amount int64) domain.Paying {
		return domain.Paying{Account: account, Kind: domain.KindSubscription, Currency: currency, PlanCode: code, Cycle: cycle, AmountMinor: amount}
	}
	founding := func(p domain.Paying, m domain.Member) domain.Paying {
		p.Founding = &m
		return p
	}
	lockedMerchant := domain.Member{DiscountBps: 2000, Currency: "EGP", PlanCode: "merchant", Cycle: domain.Monthly, LockedMinor: 29900}
	agencyPlan := domain.Paying{Account: agency, Kind: domain.KindAgency, Currency: "EGP", PlanCode: "agency", Cycle: domain.Monthly, AmountMinor: 500000}

	for _, tt := range []struct {
		name   string
		paying []domain.Paying
		want   []domain.Revenue
	}{
		{"none", nil, []domain.Revenue{}},
		{"monthly", []domain.Paying{sub(a, "EGP", "merchant", domain.Monthly, 29900)}, []domain.Revenue{{Currency: "EGP", MRRMinor: 29900, Accounts: 1}}},
		{"annual over 12", []domain.Paying{sub(a, "EGP", "merchant", domain.Annual, 299000)}, []domain.Revenue{{Currency: "EGP", MRRMinor: 24917, Accounts: 1}}},
		{
			"each currency on its own, EGP then SAR then AED",
			[]domain.Paying{sub(a, "AED", "pro", domain.Monthly, 9900), sub(b, "SAR", "pro", domain.Monthly, 9900), sub(gen.New(), "EGP", "pro", domain.Monthly, 59900)},
			[]domain.Revenue{{Currency: "EGP", MRRMinor: 59900, Accounts: 1}, {Currency: "SAR", MRRMinor: 9900, Accounts: 1}, {Currency: "AED", MRRMinor: 9900, Accounts: 1}},
		},
		{
			"a founding member pays its locked price less its discount",
			[]domain.Paying{founding(sub(a, "EGP", "merchant", domain.Monthly, 39900), lockedMerchant)},
			[]domain.Revenue{{Currency: "EGP", MRRMinor: 23920, Accounts: 1}}, // 29900 − 20%
		},
		{
			"a founding member on another plan: the current price, less its discount",
			[]domain.Paying{founding(sub(a, "EGP", "pro", domain.Monthly, 59900), lockedMerchant)},
			[]domain.Revenue{{Currency: "EGP", MRRMinor: 47920, Accounts: 1}},
		},
		{
			"an agency's flat price counts once for all its stores",
			[]domain.Paying{agencyPlan, agencyPlan, agencyPlan, sub(a, "EGP", "merchant", domain.Monthly, 29900)},
			[]domain.Revenue{{Currency: "EGP", MRRMinor: 529900, Accounts: 2}},
		},
		{
			"a founding membership never discounts an agency",
			[]domain.Paying{founding(agencyPlan, lockedMerchant)},
			[]domain.Revenue{{Currency: "EGP", MRRMinor: 500000, Accounts: 1}},
		},
		{
			"free and pay-as-you-sell plans bring no recurring revenue",
			[]domain.Paying{{Account: a, Kind: domain.KindFree, Currency: "EGP"}, {Account: b, Kind: domain.KindPays, Currency: "EGP", AmountMinor: 100}},
			[]domain.Revenue{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.MRR(tt.paying); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MRR = %+v; want %+v", got, tt.want)
			}
		})
	}
}

// TestToday (ADR-0081): the same revenue as MRR, each plan's share of it
// per currency (the most first), and the paying accounts once each.
func TestToday(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	a, b, c, agency := gen.New(), gen.New(), gen.New(), gen.New()
	sub := func(account ulid.ID, currency, code string, cycle domain.Cycle, amount int64) domain.Paying {
		return domain.Paying{Account: account, Kind: domain.KindSubscription, Currency: currency, PlanCode: code, Cycle: cycle, AmountMinor: amount}
	}
	agencyPlan := domain.Paying{Account: agency, Kind: domain.KindAgency, Currency: "EGP", PlanCode: "agency", Cycle: domain.Monthly, AmountMinor: 500000}
	paying := []domain.Paying{
		sub(a, "EGP", "merchant", domain.Monthly, 29900),
		sub(b, "EGP", "pro", domain.Annual, 599000),
		sub(c, "SAR", "pro", domain.Monthly, 9900),
		agencyPlan, agencyPlan,
		{Account: gen.New(), Kind: domain.KindFree, Currency: "EGP"},
	}

	got := domain.Today(paying)
	if !reflect.DeepEqual(got.Revenue, domain.MRR(paying)) {
		t.Fatalf("revenue = %+v; want MRR's %+v", got.Revenue, domain.MRR(paying))
	}
	want := []domain.PlanRevenue{
		{Currency: "EGP", PlanCode: "agency", MRRMinor: 500000, Accounts: 1},
		{Currency: "EGP", PlanCode: "pro", MRRMinor: 49917, Accounts: 1},
		{Currency: "EGP", PlanCode: "merchant", MRRMinor: 29900, Accounts: 1},
		{Currency: "SAR", PlanCode: "pro", MRRMinor: 9900, Accounts: 1},
	}
	if !reflect.DeepEqual(got.Plans, want) {
		t.Fatalf("plans = %+v; want %+v", got.Plans, want)
	}
	if len(got.Accounts) != 4 || !slices.Contains(got.Accounts, agency) || slices.Contains(got.Accounts, ulid.ID{}) {
		t.Fatalf("accounts = %v", got.Accounts)
	}
	if empty := domain.Today(nil); len(empty.Revenue) != 0 || len(empty.Plans) != 0 || len(empty.Accounts) != 0 {
		t.Fatalf("none = %+v", empty)
	}
}
