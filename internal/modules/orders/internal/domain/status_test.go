package domain_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestTransitions is the P4 acceptance table: every one of the 64
// (from, to) pairs, written out by hand so the test does not share the
// implementation's map. Allowed cells name their stock effect and
// permission; every other cell must be a *TransitionError.
func TestTransitions(t *testing.T) {
	t.Parallel()
	const (
		P = domain.StatusPending
		C = domain.StatusConfirmed
		R = domain.StatusReadyToShip
		S = domain.StatusShipped
		D = domain.StatusDelivered
		X = domain.StatusCancelled
		F = domain.StatusFailedDelivery
		B = domain.StatusReturned // back
	)
	type cell struct {
		ok    bool
		stock domain.StockEffect
		perm  authz.Permission
	}
	allow := func(s domain.StockEffect, p authz.Permission) cell { return cell{ok: true, stock: s, perm: p} }
	no := cell{}
	manage, fulfil := authz.PermOrdersManage, authz.PermOrdersFulfil

	tests := []struct {
		from, to domain.Status
		want     cell
	}{
		// from pending
		{P, P, no}, {P, C, allow(domain.StockNone, manage)}, {P, R, no}, {P, S, no},
		{P, D, no}, {P, X, allow(domain.StockRelease, manage)}, {P, F, no}, {P, B, no},
		// from confirmed
		{C, P, no}, {C, C, no}, {C, R, allow(domain.StockNone, fulfil)}, {C, S, no},
		{C, D, no}, {C, X, allow(domain.StockRelease, manage)}, {C, F, no}, {C, B, no},
		// from ready_to_ship
		{R, P, no}, {R, C, no}, {R, R, no}, {R, S, allow(domain.StockCommit, fulfil)},
		{R, D, no}, {R, X, allow(domain.StockRelease, manage)}, {R, F, no}, {R, B, no},
		// from shipped: no cancelling goods that left
		{S, P, no}, {S, C, no}, {S, R, no}, {S, S, no},
		{S, D, allow(domain.StockNone, fulfil)}, {S, X, no}, {S, F, allow(domain.StockNone, fulfil)}, {S, B, no},
		// from delivered
		{D, P, no}, {D, C, no}, {D, R, no}, {D, S, no},
		{D, D, no}, {D, X, no}, {D, F, no}, {D, B, allow(domain.StockRestock, fulfil)},
		// from cancelled: final
		{X, P, no}, {X, C, no}, {X, R, no}, {X, S, no},
		{X, D, no}, {X, X, no}, {X, F, no}, {X, B, no},
		// from failed_delivery: retry or return
		{F, P, no}, {F, C, no}, {F, R, no}, {F, S, allow(domain.StockCommit, fulfil)},
		{F, D, no}, {F, X, no}, {F, F, no}, {F, B, allow(domain.StockRestock, fulfil)},
		// from returned: final
		{B, P, no}, {B, C, no}, {B, R, no}, {B, S, no},
		{B, D, no}, {B, X, no}, {B, F, no}, {B, B, no},
	}
	if n := len(domain.Statuses()); len(tests) != n*n {
		t.Fatalf("table has %d cells, want %d", len(tests), n*n)
	}
	seen := map[[2]domain.Status]bool{}
	for _, tt := range tests {
		seen[[2]domain.Status{tt.from, tt.to}] = true
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			t.Parallel()
			step, err := domain.Transition(tt.from, tt.to)
			if !tt.want.ok {
				var te *domain.TransitionError
				if !errors.As(err, &te) || te.From != tt.from || te.To != tt.to ||
					!errors.Is(err, domain.ErrInvalidTransition) || !errs.Is(err, errs.KindConflict) {
					t.Fatalf("forbidden move: err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("allowed move: %v", err)
			}
			if step.From != tt.from || step.To != tt.to || step.Stock != tt.want.stock || step.Permission != tt.want.perm {
				t.Fatalf("step = %+v, want %+v", step, tt.want)
			}
		})
	}
	if len(seen) != len(tests) {
		t.Fatalf("table repeats a cell: %d distinct of %d", len(seen), len(tests))
	}
}

func TestFinalAndParse(t *testing.T) {
	t.Parallel()
	for _, s := range domain.Statuses() {
		final := s == domain.StatusCancelled || s == domain.StatusReturned
		if s.Final() != final {
			t.Errorf("%s.Final() = %v", s, s.Final())
		}
		if got, err := domain.ParseStatus(string(s)); err != nil || got != s {
			t.Errorf("ParseStatus(%q) = %q, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "Pending", "paid", "pending "} {
		if _, err := domain.ParseStatus(bad); !errs.Is(err, errs.KindValidation) {
			t.Errorf("ParseStatus(%q): %v", bad, err)
		}
	}
	err := &domain.TransitionError{From: domain.StatusShipped, To: domain.StatusCancelled}
	if err.Error() != "orders: cannot move an order from shipped to cancelled" {
		t.Errorf("message %q", err.Error())
	}
}

// TestCountsAsSale: an order counts towards bestsellers from its
// confirmation until it is cancelled, fails delivery or comes back.
func TestCountsAsSale(t *testing.T) {
	t.Parallel()
	for s, want := range map[domain.Status]bool{
		domain.StatusPending: false, domain.StatusConfirmed: true, domain.StatusReadyToShip: true, domain.StatusShipped: true,
		domain.StatusDelivered: true, domain.StatusCancelled: false, domain.StatusFailedDelivery: false, domain.StatusReturned: false,
	} {
		if got := domain.CountsAsSale(s); got != want {
			t.Errorf("%s: %v, want %v", s, got, want)
		}
	}
}

func TestParseReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason string
		to     domain.Status
		want   domain.OutcomeReason
		code   string // the field error's code, "" for none
	}{
		{"", domain.StatusConfirmed, domain.ReasonNone, ""},
		{"", domain.StatusCancelled, domain.ReasonNone, ""},
		{"refused", domain.StatusCancelled, domain.ReasonRefused, ""},
		{"refused", domain.StatusReturned, domain.ReasonRefused, ""},
		{"no_answer", domain.StatusFailedDelivery, domain.ReasonNoAnswer, ""},
		{"no_answer", domain.StatusCancelled, domain.ReasonNoAnswer, ""},
		{"refused", domain.StatusDelivered, "", "not_allowed"},
		{"no_answer", domain.StatusConfirmed, "", "not_allowed"},
		{"unconfirmed", domain.StatusCancelled, "", "invalid"}, // the confirmation's own
		{"fake", domain.StatusCancelled, "", "invalid"},
	}
	for _, tt := range tests {
		got, err := domain.ParseReason(tt.reason, tt.to)
		var e *errs.Error
		switch {
		case tt.code == "" && (err != nil || got != tt.want):
			t.Errorf("%q to %s = %q, %v; want %q", tt.reason, tt.to, got, err, tt.want)
		case tt.code != "" && (!errors.As(err, &e) || len(e.Fields) != 1 || e.Fields[0].Field != "reason" || e.Fields[0].Code != tt.code):
			t.Errorf("%q to %s: err = %v, want reason/%s", tt.reason, tt.to, err, tt.code)
		}
	}
}

// FuzzParseReason: any input is a known reason allowed on the move, or a
// validation error on reason; never a panic or an unknown reason.
func FuzzParseReason(f *testing.F) {
	for _, s := range []string{"", "refused", "no_answer", "unconfirmed", "REFUSED", "refused\x00", "رفض"} {
		f.Add(s, "cancelled")
		f.Add(s, "confirmed")
	}
	f.Fuzz(func(t *testing.T, reason, to string) {
		r, err := domain.ParseReason(reason, domain.Status(to))
		if err != nil {
			if !errs.Is(err, errs.KindValidation) || r != domain.ReasonNone {
				t.Fatalf("%q to %q: %q, %v", reason, to, r, err)
			}
			return
		}
		switch r {
		case domain.ReasonNone:
		case domain.ReasonRefused, domain.ReasonNoAnswer:
			if !domain.TakesReason(domain.Status(to)) {
				t.Fatalf("%q accepted on a move to %q", reason, to)
			}
		default:
			t.Fatalf("%q parsed as %q", reason, r)
		}
	})
}
