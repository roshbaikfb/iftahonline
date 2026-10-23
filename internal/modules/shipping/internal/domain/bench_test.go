package domain_test

import (
	"strconv"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// BenchmarkQuote prices a delivery on the storefront path (every quote
// and every order): a store with one zone per governorate, the last
// holding 200 city rates, quoting a city near the end of that list.
func BenchmarkQuote(b *testing.B) {
	areas := geo.Areas(geo.EG)
	cfg := domain.Config{Currency: money.EGP, Settings: domain.Settings{CODFee: domain.CODFee{FixedMinor: 500, BP: 100}}}
	for i, a := range areas {
		in := domain.ZoneInput{Name: "z", Areas: []string{a.Code}, RateMinor: int64(1000 + i)}
		if i == len(areas)-1 {
			for c := range domain.MaxCityRates {
				in.Cities = append(in.Cities, domain.CityInput{Name: "مدينة رقم " + strconv.Itoa(c), RateMinor: 900})
			}
		}
		z, err := domain.ValidateZone(in, geo.EG)
		if err != nil {
			b.Fatal(err)
		}
		cfg.Zones = append(cfg.Zones, z)
	}
	req := domain.QuoteRequest{Governorate: areas[len(areas)-1].Code, City: "مدينة رقم 190", Subtotal: egp(b, 45000)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cfg.Quote(req); err != nil {
			b.Fatal(err)
		}
	}
}
