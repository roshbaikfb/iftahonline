package domain

import (
	"cmp"
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Paying is a subscription that pays and renews, at what its next
// renewal is charged before any founding discount (ADR-0075).
type Paying struct {
	Account     ulid.ID // the store's own account, or its agency's
	Kind        Kind
	Currency    string
	PlanCode    string
	Cycle       Cycle
	AmountMinor int64
	// Founding is the store's membership while it keeps it, else nil.
	Founding *Member
}

// Revenue is the monthly recurring revenue in one currency.
type Revenue struct {
	Currency string
	MRRMinor int64
	// Accounts are the paying accounts it comes from: an agency is one.
	Accounts int64
}

// MonthlyShare is amount for cycle as a month's share: an annual price over
// 12, rounded half up.
func MonthlyShare(amount int64, cycle Cycle) int64 {
	if cycle == Annual {
		return (amount + 6) / 12
	}
	return amount
}

// MRR sums paying subscriptions into monthly recurring revenue per
// currency, EGP then SAR then AED, a currency with none left out. Each
// renews at its price, a founding member's at what its next invoice
// charges after its discount (Member.Renewal, as the invoice does). An
// agency's plan is one flat price for all its stores, counted once.
func MRR(paying []Paying) []Revenue { return Today(paying).Revenue }

// PlanRevenue is one plan's share of the recurring revenue in one
// currency (the console's growth page, ADR-0081).
type PlanRevenue struct {
	Currency string
	PlanCode string
	MRRMinor int64
	Accounts int64
}

// RevenueToday is what pays today: the revenue per currency (MRR), each
// plan's share of it per currency, the most first, and the paying
// accounts, each once.
type RevenueToday struct {
	Revenue  []Revenue
	Plans    []PlanRevenue
	Accounts []ulid.ID
}

// currencies are the order the console lists them in.
var currencies = []string{"EGP", "SAR", "AED"} //nolint:gochecknoglobals // a fixed list

// Today counts paying subscriptions the way MRR does, by currency and by
// plan.
func Today(paying []Paying) RevenueToday {
	sums := map[string]*Revenue{}
	plans := map[[2]string]*PlanRevenue{}
	agencies := map[ulid.ID]bool{}
	accounts := map[ulid.ID]bool{}
	for _, p := range paying {
		if !p.Kind.Paid() {
			continue
		}
		if p.Kind == KindAgency {
			if agencies[p.Account] {
				continue
			}
			agencies[p.Account] = true
		}
		amount := p.AmountMinor
		if p.Founding != nil && p.Kind == KindSubscription {
			charged, discount, _ := p.Founding.Renewal(p.PlanCode, p.Cycle, p.Currency, p.AmountMinor)
			amount = charged - discount
		}
		share := MonthlyShare(amount, p.Cycle)
		r := sums[p.Currency]
		if r == nil {
			r = &Revenue{Currency: p.Currency}
			sums[p.Currency] = r
		}
		r.MRRMinor += share
		r.Accounts++
		key := [2]string{p.Currency, p.PlanCode}
		pr := plans[key]
		if pr == nil {
			pr = &PlanRevenue{Currency: p.Currency, PlanCode: p.PlanCode}
			plans[key] = pr
		}
		pr.MRRMinor += share
		pr.Accounts++
		accounts[p.Account] = true
	}
	out := RevenueToday{Revenue: make([]Revenue, 0, len(sums)), Plans: make([]PlanRevenue, 0, len(plans)), Accounts: make([]ulid.ID, 0, len(accounts))}
	for _, c := range currencies {
		if r := sums[c]; r != nil {
			out.Revenue = append(out.Revenue, *r)
		}
	}
	for c, r := range sums {
		if !slices.Contains(currencies, c) {
			out.Revenue = append(out.Revenue, *r)
		}
	}
	for _, pr := range plans {
		out.Plans = append(out.Plans, *pr)
	}
	rank := func(c string) int {
		if i := slices.Index(currencies, c); i >= 0 {
			return i
		}
		return len(currencies)
	}
	slices.SortFunc(out.Plans, func(x, y PlanRevenue) int {
		if d := cmp.Compare(rank(x.Currency), rank(y.Currency)); d != 0 {
			return d
		}
		if d := cmp.Compare(x.Currency, y.Currency); d != 0 {
			return d
		}
		if d := cmp.Compare(y.MRRMinor, x.MRRMinor); d != 0 {
			return d
		}
		return cmp.Compare(x.PlanCode, y.PlanCode)
	})
	for a := range accounts {
		out.Accounts = append(out.Accounts, a)
	}
	slices.SortFunc(out.Accounts, func(x, y ulid.ID) int { return cmp.Compare(x.String(), y.String()) })
	return out
}
