package country_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/country"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"EG", "EG", true},
		{" sa ", "SA", true},
		{"gb", "GB", true},
		{"US", "US", true},
		{"ZZ", "", false},  // unknown region
		{"EU", "", false},  // a macro-region, not a country
		{"QO", "", false},  // private use
		{"EGY", "", false}, // alpha-3
		{"E1", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got, ok := country.Normalize(tt.in); got != tt.want || ok != tt.ok {
			t.Errorf("Normalize(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"EG", "gb", "ZZ", "", "É"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if c, ok := country.Normalize(s); ok {
			if again, ok2 := country.Normalize(c); !ok2 || again != c || len(c) != 2 {
				t.Fatalf("Normalize(%q) = %q not stable", s, c)
			}
		}
	})
}
