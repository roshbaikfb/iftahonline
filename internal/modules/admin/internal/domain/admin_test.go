package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestExpiresAt(t *testing.T) {
	t.Parallel()
	c := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if got := domain.ExpiresAt(c, c); !got.Equal(c.Add(30 * time.Minute)) {
		t.Fatalf("fresh = %v", got)
	}
	if got := domain.ExpiresAt(c, c.Add(7*time.Hour+50*time.Minute)); !got.Equal(c.Add(8 * time.Hour)) {
		t.Fatalf("capped = %v", got)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	if err := domain.ValidatePassword("a long admin passphrase"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "short pass", strings.Repeat("x", 129), "\xff\xfe\xfd invalid utf8 x"} {
		if err := domain.ValidatePassword(bad); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("password %q: %v", bad, err)
		}
	}
	if n, err := domain.ValidateName("  Ops Team "); err != nil || n != "Ops Team" {
		t.Fatalf("name = %q, %v", n, err)
	}
	for _, bad := range []string{"", " ", strings.Repeat("a", 101), "a\x00b"} {
		if _, err := domain.ValidateName(bad); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("name %q: %v", bad, err)
		}
	}
	if r, err := domain.ValidateReason("  fraud report #123\nconfirmed "); err != nil || r != "fraud report #123\nconfirmed" {
		t.Fatalf("reason = %q, %v", r, err)
	}
	for _, bad := range []string{"", "no", strings.Repeat("r", 501), "bell\a"} {
		if _, err := domain.ValidateReason(bad); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("reason %q: %v", bad, err)
		}
	}
}
