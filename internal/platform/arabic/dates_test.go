package arabic_test

import (
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
)

func TestDates(t *testing.T) {
	t.Parallel()
	riyadh := time.FixedZone("AST", 3*3600)
	for _, tt := range []struct {
		at         time.Time
		date, when string
	}{
		{time.Date(2026, 11, 8, 14, 30, 0, 0, riyadh), "8 نوفمبر 2026", "الأحد 8 نوفمبر 2:30 م"},
		{time.Date(2026, 1, 3, 0, 5, 0, 0, time.UTC), "3 يناير 2026", "السبت 3 يناير 12:05 ص"},
		{time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC), "31 ديسمبر 2026", "الخميس 31 ديسمبر 12:00 م"},
	} {
		if got := arabic.Date(tt.at); got != tt.date {
			t.Errorf("Date(%v) = %q; want %q", tt.at, got, tt.date)
		}
		if got := arabic.Moment(tt.at); got != tt.when {
			t.Errorf("Moment(%v) = %q; want %q", tt.at, got, tt.when)
		}
	}
}
