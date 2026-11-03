package domain

import (
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
func MRR(paying []Paying) []Revenue {
	sums := map[string]*Revenue{}
	agencies := map[ulid.ID]bool{}
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
		r := sums[p.Currency]
		if r == nil {
			r = &Revenue{Currency: p.Currency}
			sums[p.Currency] = r
		}
		r.MRRMinor += MonthlyShare(amount, p.Cycle)
		r.Accounts++
	}
	currencies := []string{"EGP", "SAR", "AED"} // the order the console lists them in
	out := make([]Revenue, 0, len(sums))
	for _, c := range currencies {
		if r := sums[c]; r != nil {
			out = append(out, *r)
		}
	}
	for c, r := range sums {
		if !slices.Contains(currencies, c) {
			out = append(out, *r)
		}
	}
	return out
}
