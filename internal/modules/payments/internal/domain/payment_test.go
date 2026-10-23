package domain_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func egp(t testing.TB, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNext(t *testing.T) {
	t.Parallel()
	pending := domain.Payment{Amount: egp(t, 52000), Refunded: egp(t, 0), Status: domain.StatusPending}
	paid := pending
	paid.Status, paid.TransactionID = domain.StatusPaid, "t1"
	tests := []struct {
		name     string
		p        domain.Payment
		w        domain.GatewayWord
		st       domain.Status
		refunded int64
		failure  bool
	}{
		{"still pending", pending, domain.GatewayWord{Amount: egp(t, 0), Refunded: egp(t, 0)}, domain.StatusPending, 0, false},
		{"paid", pending, domain.GatewayWord{Paid: true, Amount: egp(t, 52000), Refunded: egp(t, 0), TransactionID: "t1"}, domain.StatusPaid, 0, false},
		{"paid another amount", pending, domain.GatewayWord{Paid: true, Amount: egp(t, 100), Refunded: egp(t, 0)}, domain.StatusPending, 0, true},
		{"declined", pending, domain.GatewayWord{Failed: true, Amount: egp(t, 0), Refunded: egp(t, 0)}, domain.StatusFailed, 0, false},
		{"partly refunded", paid, domain.GatewayWord{Paid: true, Amount: egp(t, 52000), Refunded: egp(t, 20000)}, domain.StatusPartiallyRefunded, 20000, false},
		{"fully refunded", paid, domain.GatewayWord{Paid: true, Amount: egp(t, 52000), Refunded: egp(t, 52000)}, domain.StatusRefunded, 52000, false},
		{"a captured payment does not fail", paid, domain.GatewayWord{Failed: true, Amount: egp(t, 0), Refunded: egp(t, 0)}, domain.StatusPaid, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, refunded, _, failure := domain.Next(tt.p, tt.w)
			if st != tt.st || refunded.Amount() != tt.refunded || (failure != "") != tt.failure {
				t.Fatalf("Next = %s %d %q", st, refunded.Amount(), failure)
			}
		})
	}
}

func TestRefundable(t *testing.T) {
	t.Parallel()
	p := domain.Payment{Amount: egp(t, 52000), Refunded: egp(t, 20000), Status: domain.StatusPartiallyRefunded}
	for requested, want := range map[int64]int64{0: 32000, 30000: 2000, 40000: 0} {
		got, err := domain.Refundable(p, egp(t, requested))
		if err != nil || got.Amount() != want {
			t.Errorf("requested %d: %v %v, want %d", requested, got, err, want)
		}
	}
	p.Status = domain.StatusPending
	if got, err := domain.Refundable(p, egp(t, 0)); err != nil || !got.IsZero() {
		t.Fatalf("pending: %v %v", got, err)
	}
	if !domain.StatusRefunded.Captured() || domain.StatusFailed.Captured() || !domain.StatusPending.Open() || domain.StatusPaid.Open() {
		t.Fatal("predicates")
	}
}

func TestCleaning(t *testing.T) {
	t.Parallel()
	if r, err := domain.CleanReason("  the parcel came back  "); err != nil || r != "the parcel came back" {
		t.Fatalf("reason %q %v", r, err)
	}
	if _, err := domain.CleanReason(strings.Repeat("x", 301)); err == nil {
		t.Fatal("long reason")
	}
	if k, err := domain.CleanKey("secret_key", " sk_live_1 ", true); err != nil || k != "sk_live_1" {
		t.Fatalf("key %q %v", k, err)
	}
	if k, err := domain.CleanKey("api_key", "", false); err != nil || k != "" {
		t.Fatalf("optional empty %q %v", k, err)
	}
	for _, bad := range []string{"", "a b", strings.Repeat("k", 513)} {
		if _, err := domain.CleanKey("secret_key", bad, true); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
