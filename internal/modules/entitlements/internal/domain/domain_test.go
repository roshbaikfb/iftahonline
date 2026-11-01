package domain_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"
)

func TestFeature(t *testing.T) {
	t.Parallel()
	for f, want := range map[domain.Feature]bool{domain.Messages: true, "": false, "sms": false, "Messages": false} {
		if f.Valid() != want {
			t.Errorf("%q.Valid() = %v", f, !want)
		}
	}
}

func TestPeriod(t *testing.T) {
	t.Parallel()
	cairo := time.FixedZone("EET", 2*3600)
	tests := []struct {
		in   time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 28, 15, 4, 5, 6, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 11, 1, 1, 0, 0, 0, cairo), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, // still October in UTC
		{time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		if got := domain.PeriodOf(tt.in); !got.Equal(tt.want) || got.Location() != time.UTC {
			t.Errorf("PeriodOf(%v) = %v, want %v", tt.in, got, tt.want)
		}
		if !domain.ValidPeriod(tt.want) {
			t.Errorf("ValidPeriod(%v) = false", tt.want)
		}
	}
	for _, p := range []time.Time{{}, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 1, 0, time.UTC)} {
		if domain.ValidPeriod(p) {
			t.Errorf("ValidPeriod(%v) = true", p)
		}
	}
}

func TestUsageLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		used, limit, n int64
		remaining      int64
		level, crossed domain.Threshold
	}{
		{used: 0, limit: 100, n: 0, remaining: 100, level: domain.NoThreshold, crossed: domain.NoThreshold},
		{used: 79, limit: 100, n: 1, remaining: 21, level: domain.NoThreshold, crossed: domain.NoThreshold},
		{used: 80, limit: 100, n: 1, remaining: 20, level: domain.Nearly, crossed: domain.Nearly},
		{used: 81, limit: 100, n: 1, remaining: 19, level: domain.Nearly, crossed: domain.NoThreshold},
		{used: 99, limit: 100, n: 1, remaining: 1, level: domain.Nearly, crossed: domain.NoThreshold},
		{used: 100, limit: 100, n: 1, remaining: 0, level: domain.Reached, crossed: domain.Reached},
		{used: 100, limit: 100, n: 25, remaining: 0, level: domain.Reached, crossed: domain.Reached}, // jumps both: the higher one
		{used: 4, limit: 5, n: 1, remaining: 1, level: domain.Nearly, crossed: domain.Nearly},
		{used: 0, limit: 0, n: 0, remaining: 0, level: domain.Reached, crossed: domain.NoThreshold},
		{used: 7, limit: 5, n: 0, remaining: 0, level: domain.Reached, crossed: domain.NoThreshold}, // limit lowered below use
	}
	for _, tt := range tests {
		u := domain.Usage{Feature: domain.Messages, Used: tt.used, Limit: tt.limit}
		if got := u.Remaining(); got != tt.remaining {
			t.Errorf("%d/%d Remaining = %d, want %d", tt.used, tt.limit, got, tt.remaining)
		}
		if got := u.Level(); got != tt.level {
			t.Errorf("%d/%d Level = %d, want %d", tt.used, tt.limit, got, tt.level)
		}
		if got := u.CrossedBy(tt.n); got != tt.crossed {
			t.Errorf("%d/%d CrossedBy(%d) = %d, want %d", tt.used, tt.limit, tt.n, got, tt.crossed)
		}
	}
}

func TestFeatureKinds(t *testing.T) {
	t.Parallel()
	all := domain.Features()
	if len(all) != 11 {
		t.Fatalf("%d enforced features, want 11", len(all))
	}
	for f, k := range all {
		if f.Kind() != k || k == "" {
			t.Errorf("%s: Kind = %q, listed as %q", f, f.Kind(), k)
		}
		if f.Valid() != (k == domain.Monthly) {
			t.Errorf("%s: Valid = %v for a %s feature", f, f.Valid(), k)
		}
	}
	for _, f := range []domain.Feature{"", "sms", "AdFree", "loyalty_points"} {
		if f.Kind() != "" || f.Valid() {
			t.Errorf("%q is not enforced, got kind %q", f, f.Kind())
		}
	}
}

func TestGrantLimit(t *testing.T) {
	t.Parallel()
	if l := (domain.Grant{Value: 7}).Limit(); l != 7 {
		t.Errorf("Limit = %d", l)
	}
	if l := (domain.Grant{Value: 7, Unlimited: true}).Limit(); l != domain.Unlimited {
		t.Errorf("unlimited Limit = %d", l)
	}
	if l := (domain.Grant{}).Limit(); l != 0 {
		t.Errorf("not granted: Limit = %d", l)
	}
}

// TestUnlimitedUsage: an unlimited feature never warns, however much is
// used (Limit*80 would overflow).
func TestUnlimitedUsage(t *testing.T) {
	t.Parallel()
	for _, used := range []int64{0, 1, 1 << 40, domain.Unlimited - 1} {
		u := domain.Usage{Feature: domain.OrdersPerMonth, Used: used, Limit: domain.Unlimited}
		if !u.Unlimited() || u.Level() != domain.NoThreshold || u.CrossedBy(1) != domain.NoThreshold {
			t.Errorf("used %d: unlimited %v, level %d", used, u.Unlimited(), u.Level())
		}
	}
	if (domain.Usage{Limit: 5}).Unlimited() {
		t.Error("a limit of 5 is not unlimited")
	}
}
