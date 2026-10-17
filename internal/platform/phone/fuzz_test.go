package phone_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// FuzzPhoneParse: Parse never panics; any accepted number is canonical
// E.164, re-parses to itself from every region (idempotent), and its mask
// never contains the full number.
func FuzzPhoneParse(f *testing.F) {
	for _, s := range []string{
		"01012345678", "+20 10 1234 5678", "0020 155 123 4567", "٠١٠١٢٣٤٥٦٧٨",
		"+966501234567", "050 123 4567", "+96551234567", "+974 7012 3456",
		"+973 6612 3456", "+968 7123 4567", "++", "\u200e", "00", "+0",
	} {
		f.Add(s, "EG")
	}
	f.Add("51234567", "KW")
	f.Fuzz(func(t *testing.T, raw, region string) {
		n, err := phone.Parse(raw, phone.Region(region))
		if err != nil {
			if !errors.Is(err, phone.ErrInvalid) && !errors.Is(err, phone.ErrUnsupportedCountry) {
				t.Fatalf("Parse(%q) unexpected error class: %v", raw, err)
			}
			return
		}
		e := n.Unmasked()
		if !strings.HasPrefix(e, "+") || len(e) < 11 || len(e) > 16 {
			t.Fatalf("Parse(%q) = %q, not E.164", raw, e)
		}
		for _, r := range phone.Regions() {
			back, err := phone.Parse(e, r)
			if err != nil || back != n {
				t.Fatalf("re-parse %q in %s = %v, %v", e, r, back.Unmasked(), err)
			}
		}
		if strings.Contains(n.Masked(), e[1:]) {
			t.Fatalf("mask %q leaks %q", n.Masked(), e)
		}
	})
}
