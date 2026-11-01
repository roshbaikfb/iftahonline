package main

import (
	"context"
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

type verdictOf struct {
	v   risk.Verdict
	err error
}

func (f verdictOf) Check(context.Context, phone.E164) (risk.Verdict, error) { return f.v, f.err }

func TestRiskRulesAdapter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   risk.Action
		want orders.RiskAction
	}{{risk.NoAction, orders.RiskNone}, {risk.Hold, orders.RiskHold}, {risk.Verify, orders.RiskVerify}, {risk.Prepay, orders.RiskPrepay}} {
		got, err := riskRules{verdictOf{v: risk.Verdict{Band: risk.BandHigh, Action: tt.in}}}.Check(t.Context(), phone.E164{})
		if err != nil || got.Action != tt.want || got.Band != "high" {
			t.Errorf("%q: %+v, %v", tt.in, got, err)
		}
	}
	boom := errors.New("boom")
	if _, err := (riskRules{verdictOf{err: boom}}).Check(t.Context(), phone.E164{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
