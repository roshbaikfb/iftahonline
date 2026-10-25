package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func egp(t testing.TB, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ptr[T any](v T) *T { return &v }

func TestCODFeeFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fee    domain.CODFee
		amount int64
		want   int64
	}{
		{"none", domain.CODFee{}, 50000, 0},
		{"none ignores the minimum", domain.CODFee{MinMinor: 500}, 50000, 0},
		{"fixed", domain.CODFee{FixedMinor: 1000}, 50000, 1000},
		{"percent", domain.CODFee{BP: 150}, 50000, 750},
		{"percent rounds half up", domain.CODFee{BP: 150}, 3333, 50}, // 49.995
		{"fixed plus percent", domain.CODFee{FixedMinor: 500, BP: 100}, 50000, 1000},
		{"minimum", domain.CODFee{BP: 100, MinMinor: 1000}, 20000, 1000},
		{"maximum", domain.CODFee{BP: 500, MaxMinor: 2000}, 100000, 2000},
		{"no maximum when 0", domain.CODFee{BP: 500}, 100000, 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.fee.For(egp(t, tt.amount))
			if err != nil || got.Amount() != tt.want || got.Currency() != money.EGP {
				t.Fatalf("For(%d) = %v, %v; want %d", tt.amount, got, err, tt.want)
			}
		})
	}
}

func zone(t *testing.T, in domain.ZoneInput) domain.Zone {
	t.Helper()
	z, err := domain.ValidateZone(in, geo.EG)
	if err != nil {
		t.Fatal(err)
	}
	z.ID = ulid.NewGenerator().New()
	return z
}

func TestQuote(t *testing.T) {
	t.Parallel()
	cairo := zone(t, domain.ZoneInput{Name: "القاهرة الكبرى", Areas: []string{"EG-C", "EG-GZ"}, RateMinor: 5000,
		Cities: []domain.CityInput{{Name: "المعادي", RateMinor: 4000}, {Name: "6 أكتوبر", RateMinor: 6500}}})
	upper := zone(t, domain.ZoneInput{Name: "الصعيد", Areas: []string{"EG-ASN", "EG-LX"}, RateMinor: 9000,
		FreeOverMinor: ptr[int64](200000), CODFee: &domain.CODFee{FixedMinor: 2000}})
	store := domain.Settings{CODFee: domain.CODFee{FixedMinor: 1000}, FreeOverMinor: ptr[int64](100000)}
	withZones := domain.Config{Currency: money.EGP, Settings: store, Zones: []domain.Zone{cairo, upper}}

	tests := []struct {
		name        string
		cfg         domain.Config
		gov, city   string
		subtotal    int64
		deliverable bool
		zone        *ulid.ID
		ship, cod   int64
		free        bool
	}{
		{"no zones: everywhere, free, store fee", domain.Config{Currency: money.EGP, Settings: domain.Settings{CODFee: domain.CODFee{BP: 100}}},
			"EG-ALX", "سموحة", 50000, true, nil, 0, 500, false},
		{"defaults: nothing at all", domain.Config{Currency: money.EGP}, "EG-ALX", "", 50000, true, nil, 0, 0, false},
		{"zone rate", withZones, "EG-C", "مدينة نصر", 50000, true, &cairo.ID, 5000, 1000, false},
		{"second governorate of the zone", withZones, "EG-GZ", "الدقي", 50000, true, &cairo.ID, 5000, 1000, false},
		{"city rate", withZones, "EG-C", "المعادي", 50000, true, &cairo.ID, 4000, 1000, false},
		{"city matched in search form", withZones, "EG-C", "  المعادى ", 50000, true, &cairo.ID, 4000, 1000, false},
		{"city of another zone does not apply", withZones, "EG-LX", "المعادي", 50000, true, &upper.ID, 9000, 2000, false},
		{"store free threshold", withZones, "EG-C", "", 100000, true, &cairo.ID, 0, 1000, true},
		{"just under the threshold", withZones, "EG-C", "", 99999, true, &cairo.ID, 5000, 1000, false},
		{"zone threshold overrides store's", withZones, "EG-LX", "", 150000, true, &upper.ID, 9000, 2000, false},
		{"zone threshold reached", withZones, "EG-ASN", "", 200000, true, &upper.ID, 0, 2000, true},
		{"governorate outside every zone", withZones, "EG-ALX", "", 50000, false, nil, 0, 0, false},
		{"no zones: store threshold", domain.Config{Currency: money.EGP, Settings: store}, "EG-ALX", "", 100000, true, nil, 0, 1000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q, err := tt.cfg.Quote(domain.QuoteRequest{Governorate: tt.gov, City: tt.city, Subtotal: egp(t, tt.subtotal)})
			if err != nil {
				t.Fatal(err)
			}
			if q.Deliverable != tt.deliverable || q.Shipping.Amount() != tt.ship || q.CODFee.Amount() != tt.cod || q.FreeShipping != tt.free ||
				(q.ZoneID == nil) != (tt.zone == nil) || (q.ZoneID != nil && *q.ZoneID != *tt.zone) {
				t.Fatalf("quote = %+v; want deliverable=%v ship=%d cod=%d free=%v zone=%v", q, tt.deliverable, tt.ship, tt.cod, tt.free, tt.zone)
			}
		})
	}

	// The COD fee is on what the courier collects: subtotal + shipping.
	pct := domain.Config{Currency: money.EGP, Settings: domain.Settings{CODFee: domain.CODFee{BP: 100}}, Zones: []domain.Zone{cairo}}
	q, err := pct.Quote(domain.QuoteRequest{Governorate: "EG-C", Subtotal: egp(t, 45000)})
	if err != nil || q.CODFee.Amount() != 500 {
		t.Fatalf("percent fee on subtotal+shipping: %+v %v, want 500", q, err)
	}
	if _, err = withZones.Quote(domain.QuoteRequest{Governorate: "EG-C", Subtotal: money.Money{}}); err == nil {
		t.Fatal("subtotal in another currency accepted")
	}
}

func TestValidateZone(t *testing.T) {
	t.Parallel()
	ok := domain.ZoneInput{Name: " الدلتا ", Areas: []string{"EG-GH", "EG-DK", "EG-GH"}, RateMinor: 6000,
		Cities: []domain.CityInput{{Name: "المنصورة", RateMinor: 5500}}}
	z, err := domain.ValidateZone(ok, geo.EG)
	if err != nil {
		t.Fatal(err)
	}
	if z.Name != "الدلتا" || strings.Join(z.Areas, ",") != "EG-DK,EG-GH" || len(z.Cities) != 1 || z.Cities[0].Key == "" {
		t.Fatalf("zone = %+v", z)
	}

	many := make([]domain.CityInput, domain.MaxCityRates+1)
	for i := range many {
		many[i] = domain.CityInput{Name: "مدينة " + strings.Repeat("ا", i%50+1), RateMinor: 1}
	}
	bad := map[string]struct {
		in    domain.ZoneInput
		field string
	}{
		"no name":           {domain.ZoneInput{Areas: []string{"EG-C"}}, "name"},
		"long name":         {domain.ZoneInput{Name: strings.Repeat("x", 61), Areas: []string{"EG-C"}}, "name"},
		"no areas":          {domain.ZoneInput{Name: "z"}, "areas"},
		"foreign area":      {domain.ZoneInput{Name: "z", Areas: []string{"SA-01"}}, "areas[0]"},
		"unknown area":      {domain.ZoneInput{Name: "z", Areas: []string{"EG-C", "EG-XX"}}, "areas[1]"},
		"negative rate":     {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: -1}, "rate_minor"},
		"huge rate":         {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: domain.MaxAmount + 1}, "rate_minor"},
		"negative free":     {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, FreeOverMinor: ptr[int64](-5)}, "free_over_minor"},
		"fee too high":      {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, CODFee: &domain.CODFee{BP: domain.MaxBP + 1}}, "cod_fee.bp"},
		"fee min above max": {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, CODFee: &domain.CODFee{FixedMinor: 1, MinMinor: 9, MaxMinor: 5}}, "cod_fee.min_minor"},
		"duplicate city": {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"},
			Cities: []domain.CityInput{{Name: "المعادي"}, {Name: "المعادى"}}}, "cities[1].name"},
		"short city":         {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Cities: []domain.CityInput{{Name: "x"}}}, "cities[0].name"},
		"city rate":          {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Cities: []domain.CityInput{{Name: "حلوان", RateMinor: -1}}}, "cities[0].rate_minor"},
		"too many cities":    {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Cities: many}, "cities"},
		"days min above max": {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Delivery: &domain.DeliveryDays{Min: 5, Max: 2}}, "eta_min_days"},
		"days negative":      {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Delivery: &domain.DeliveryDays{Min: -1, Max: 2}}, "eta_min_days"},
		"days too many":      {domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Delivery: &domain.DeliveryDays{Min: 1, Max: domain.MaxDeliveryDays + 1}}, "eta_max_days"},
	}
	for name, tt := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ValidateZone(tt.in, geo.EG)
			if !hasField(err, tt.field) {
				t.Fatalf("err = %v, want a field error on %s", err, tt.field)
			}
		})
	}
}

// TestZoneDeliveryDays: a zone may say how long delivery takes; it is
// kept as typed, and a quote to that zone carries it.
func TestZoneDeliveryDays(t *testing.T) {
	t.Parallel()
	for _, d := range []domain.DeliveryDays{{Min: 2, Max: 3}, {Min: 0, Max: 0}, {Min: 1, Max: domain.MaxDeliveryDays}} {
		z, err := domain.ValidateZone(domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, Delivery: &d}, geo.EG)
		if err != nil || z.Delivery == nil || *z.Delivery != d {
			t.Fatalf("%+v: zone %+v, %v", d, z.Delivery, err)
		}
	}
	none, err := domain.ValidateZone(domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}}, geo.EG)
	if err != nil || none.Delivery != nil {
		t.Fatalf("a zone without days: %+v, %v", none.Delivery, err)
	}

	days := &domain.DeliveryDays{Min: 2, Max: 3}
	cairo := zone(t, domain.ZoneInput{Name: "القاهرة", Areas: []string{"EG-C"}, RateMinor: 5000, Delivery: days})
	far := zone(t, domain.ZoneInput{Name: "الصعيد", Areas: []string{"EG-ASN"}, RateMinor: 9000, FreeOverMinor: ptr[int64](200000)})
	cfg := domain.Config{Currency: money.EGP, Settings: domain.Settings{FreeOverMinor: ptr[int64](100000)}, Zones: []domain.Zone{cairo, far}}
	q, err := cfg.Quote(domain.QuoteRequest{Governorate: "EG-C", Subtotal: egp(t, 24000)})
	if err != nil || q.Delivery == nil || *q.Delivery != *days || q.FreeOverMinor == nil || *q.FreeOverMinor != 100000 {
		t.Fatalf("quote to Cairo: %+v, %v (days 2-3; the store's free threshold)", q, err)
	}
	q, err = cfg.Quote(domain.QuoteRequest{Governorate: "EG-ASN", Subtotal: egp(t, 24000)})
	if err != nil || q.Delivery != nil || q.FreeOverMinor == nil || *q.FreeOverMinor != 200000 {
		t.Fatalf("quote to Aswan: %+v, %v (no days; the zone's threshold)", q, err)
	}
	q, err = domain.Config{Currency: money.EGP}.Quote(domain.QuoteRequest{Governorate: "EG-C", Subtotal: egp(t, 24000)})
	if err != nil || q.Delivery != nil || q.FreeOverMinor != nil {
		t.Fatalf("no zones, no threshold: %+v, %v", q, err)
	}
}

func TestValidateSettings(t *testing.T) {
	t.Parallel()
	if err := domain.ValidateSettings(domain.Settings{CODFee: domain.CODFee{FixedMinor: 1000, BP: 100, MinMinor: 500, MaxMinor: 3000},
		FreeOverMinor: ptr[int64](0)}); err != nil {
		t.Fatal(err)
	}
	for field, s := range map[string]domain.Settings{
		"cod_fee.fixed_minor": {CODFee: domain.CODFee{FixedMinor: -1}},
		"cod_fee.max_minor":   {CODFee: domain.CODFee{MaxMinor: domain.MaxAmount + 1}},
		"cod_fee.bp":          {CODFee: domain.CODFee{BP: -1}},
		"free_over_minor":     {FreeOverMinor: ptr[int64](domain.MaxAmount + 1)},
	} {
		if err := domain.ValidateSettings(s); !hasField(err, field) {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func hasField(err error, field string) bool {
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		return false
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}

// FuzzQuote: any typed city and governorate quote without error, and a
// quote is never negative.
func FuzzQuote(f *testing.F) {
	z, err := domain.ValidateZone(domain.ZoneInput{Name: "z", Areas: []string{"EG-C"}, RateMinor: 5000,
		Cities: []domain.CityInput{{Name: "المعادي", RateMinor: 4000}}}, geo.EG)
	if err != nil {
		f.Fatal(err)
	}
	cfg := domain.Config{Currency: money.EGP, Settings: domain.Settings{CODFee: domain.CODFee{FixedMinor: 100, BP: 150}}, Zones: []domain.Zone{z}}
	f.Add("EG-C", "المعادي", int64(50000))
	f.Add("EG-ALX", "", int64(0))
	f.Add("", "\x00\u200f", int64(domain.MaxAmount))
	f.Fuzz(func(t *testing.T, gov, city string, sub int64) {
		if sub < 0 || sub > domain.MaxAmount*100 {
			return
		}
		q, err := cfg.Quote(domain.QuoteRequest{Governorate: gov, City: city, Subtotal: egp(t, sub)})
		if err != nil {
			t.Fatal(err)
		}
		if q.Shipping.IsNegative() || q.CODFee.IsNegative() || (gov != "EG-C" && q.Deliverable) {
			t.Fatalf("quote %+v for %q/%q", q, gov, city)
		}
	})
}
