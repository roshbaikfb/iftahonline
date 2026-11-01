package main

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// orderRisk builds the store's risk rules as orders ask them before
// placing a storefront cash order (ADR-0066).
func orderRisk(c clients) (orders.RiskCheck, error) {
	chk, err := risk.NewChecker(risk.CheckerDeps{Pool: c.pool, PhoneKey: c.cfg.Secrets.RiskPhoneKey, Plan: c.plan})
	if err != nil {
		return nil, err
	}
	return riskRules{chk}, nil
}

// riskRules adapts risk.Checker to orders.RiskCheck.
type riskRules struct{ c risk.Checker }

func (r riskRules) Check(ctx context.Context, p phone.E164) (orders.Risk, error) {
	v, err := r.c.Check(ctx, p)
	if err != nil {
		return orders.Risk{}, err
	}
	out := orders.Risk{Band: string(v.Band)}
	switch v.Action {
	case risk.Hold:
		out.Action = orders.RiskHold
	case risk.Verify:
		out.Action = orders.RiskVerify
	case risk.NoAction:
	}
	return out, nil
}
