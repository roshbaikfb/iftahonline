package main

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// TestOrderInfoCOD: the courier collects what was not paid online
// (ADR-0029, ADR-0067).
func TestOrderInfoCOD(t *testing.T) {
	t.Parallel()
	egp := func(n int64) money.Money {
		m, err := money.New(n, money.EGP)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tt := range []struct {
		name string
		o    orders.Order
		want int64
	}{
		{"cash", orders.Order{PaymentMethod: orders.PaymentCOD, Total: egp(51500)}, 51500},
		{"card", orders.Order{PaymentMethod: orders.PaymentCard, Total: egp(45000)}, 0},
		{"deposit awaiting", orders.Order{PaymentMethod: orders.PaymentCOD, Total: egp(51500), Deposit: egp(6500), DepositStatus: orders.PaymentAwaiting}, 51500},
		{"deposit paid", orders.Order{PaymentMethod: orders.PaymentCOD, Total: egp(51500), Deposit: egp(6500), DepositStatus: orders.PaymentPaid}, 45000},
	} {
		got, err := orderInfo(tt.o, nil)
		if err != nil || got.COD.Amount() != tt.want {
			t.Errorf("%s: COD %v, %v; want %d", tt.name, got.COD, err, tt.want)
		}
	}
	if _, err := orderInfo(orders.Order{}, orders.ErrOrderNotFound); !errors.Is(err, shipping.ErrOrderUnknown) {
		t.Fatalf("unknown order: %v", err)
	}
}
