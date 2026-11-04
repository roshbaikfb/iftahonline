package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// AdminGetGrowth implements StrictServerInterface (ADR-0081).
func (h *Handlers) AdminGetGrowth(ctx context.Context, _ AdminGetGrowthRequestObject) (AdminGetGrowthResponseObject, error) {
	g, err := h.svc.Growth(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminGetGrowth200JSONResponse{ComputedAt: g.ComputedAt, Months: make([]openapi_types.Date, len(g.Months)),
		Errors: make([]AdminGrowthErrors, len(g.Errors)), Paying: g.Paying}
	for i, m := range g.Months {
		out.Months[i] = openapi_types.Date{Time: m}
	}
	for i, e := range g.Errors {
		out.Errors[i] = AdminGrowthErrors(e)
	}
	if g.Collected != nil {
		series := make([]AdminCollectedSeries, len(g.Collected))
		for i, c := range g.Collected {
			months := make([]AdminMonthAmount, len(c.Months))
			for j, m := range c.Months {
				months[j] = AdminMonthAmount{Month: openapi_types.Date{Time: m.Month}, Invoices: m.Invoices, AmountMinor: m.AmountMinor}
			}
			series[i] = AdminCollectedSeries{Currency: AdminCollectedSeriesCurrency(c.Currency), Months: months}
		}
		out.Collected = &series
	}
	if g.Stores != nil {
		stores := make([]AdminMonthStores, len(g.Stores))
		for i, s := range g.Stores {
			stores[i] = AdminMonthStores{Month: openapi_types.Date{Time: s.Month}, Created: s.Created, Converted: s.Converted}
		}
		out.Stores = &stores
	}
	if g.MRR != nil {
		days := make([]AdminDayMRR, len(g.MRR))
		for i, d := range g.MRR {
			days[i] = AdminDayMRR{Day: openapi_types.Date{Time: d.Day}, Currency: AdminDayMRRCurrency(d.Currency), MrrMinor: d.MRRMinor, Accounts: d.Accounts}
		}
		out.Mrr = &days
	}
	if g.Churn != nil {
		months := make([]AdminMonthChurn, len(g.Churn))
		for i, c := range g.Churn {
			months[i] = AdminMonthChurn{Month: openapi_types.Date{Time: c.Month}, Start: c.Start, Joined: c.Joined, Lost: c.Lost, RateBps: c.RateBps()}
		}
		out.Churn = &months
	}
	if g.Plans != nil {
		plans := make([]AdminPlanRevenue, len(g.Plans))
		for i, p := range g.Plans {
			plans[i] = AdminPlanRevenue{Currency: AdminPlanRevenueCurrency(p.Currency), Plan: p.PlanCode, Accounts: p.Accounts, MrrMinor: p.MRRMinor}
		}
		out.Plans = &plans
	}
	return out, nil
}
