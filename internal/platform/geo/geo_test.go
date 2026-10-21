package geo_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func TestAreas(t *testing.T) {
	t.Parallel()
	want := map[geo.Country]int{geo.EG: 27, geo.SA: 13, geo.AE: 7, geo.KW: 6, geo.QA: 8, geo.BH: 4, geo.OM: 11}
	for c, n := range want {
		areas := geo.Areas(c)
		if len(areas) != n {
			t.Errorf("%s: %d areas, want %d", c, len(areas), n)
		}
		seen := map[string]bool{}
		for _, a := range areas {
			if !strings.HasPrefix(a.Code, string(c)+"-") || seen[a.Code] || a.NameAR == "" || a.NameEN == "" {
				t.Errorf("%s: bad or duplicate area %+v", c, a)
			}
			seen[a.Code] = true
		}
	}
	if geo.Areas("FR") != nil {
		t.Error("unsupported country has areas")
	}
	// Callers may not change the list for others.
	geo.Areas(geo.EG)[0].Code = "X"
	if geo.Areas(geo.EG)[0].Code == "X" {
		t.Error("Areas shares its slice")
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		country geo.Country
		code    string
		ok      bool
		ar, en  string
	}{
		{geo.EG, "EG-C", true, "القاهرة", "Cairo"},
		{geo.EG, " eg-gz ", true, "الجيزة", "Giza"},
		{geo.SA, "SA-01", true, "الرياض", "Riyadh"},
		{geo.AE, "AE-DU", true, "دبي", "Dubai"},
		{geo.EG, "SA-01", false, "", ""}, // another country's area
		{geo.EG, "EG-XX", false, "", ""},
		{geo.EG, "", false, "", ""},
		{"FR", "FR-75", false, "", ""},
	}
	for _, tt := range tests {
		a, ok := geo.Lookup(tt.country, tt.code)
		if ok != tt.ok || a.Name("ar") != tt.ar || a.Name("en") != tt.en {
			t.Errorf("Lookup(%s, %q) = %+v, %v", tt.country, tt.code, a, ok)
		}
	}
}

func TestCountryOf(t *testing.T) {
	t.Parallel()
	for _, cur := range money.Currencies() {
		c, ok := geo.CountryOf(cur)
		if !ok || len(geo.Areas(c)) == 0 {
			t.Errorf("CountryOf(%s) = %q, %v", cur, c, ok)
		}
	}
	if _, ok := geo.CountryOf("USD"); ok {
		t.Error("USD has a country")
	}
}
