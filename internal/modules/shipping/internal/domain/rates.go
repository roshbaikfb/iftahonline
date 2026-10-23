// Package domain holds the shipping rules (ADR-0025): delivery zones made
// of governorates, a flat rate per zone with per-city exceptions, free
// shipping above an order value, and the cash-on-delivery fee; and the
// quote that applies them to an order.
package domain

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits.
const (
	MaxCityRates   = 200 // per zone
	MaxZoneNameLen = 60
	MinCityLen     = 2
	MaxCityLen     = 80 // as an order's city
	// MaxAmount bounds every rate and threshold (10 million in major
	// units): far above any real fee, far below overflow.
	MaxAmount = 1_000_000_000
	// MaxBP is the largest percentage COD fee: 10%.
	MaxBP = 1000
)

// ErrNotFound is returned by the store when no row matches.
var ErrNotFound = errors.New("shipping: not found")

// CODFee is the cash-on-delivery fee: a fixed part plus a percentage of
// the amount collected (the order's subtotal and shipping), kept between
// a minimum and a maximum. The zero value charges nothing.
type CODFee struct {
	FixedMinor int64
	BP         int64 // basis points: 150 = 1.5%
	MinMinor   int64
	MaxMinor   int64 // 0: no maximum
}

// IsZero reports whether the fee charges nothing.
func (f CODFee) IsZero() bool { return f.FixedMinor == 0 && f.BP == 0 }

// For returns the fee on amount (rounded half up to the minor unit).
func (f CODFee) For(amount money.Money) (money.Money, error) {
	if f.IsZero() {
		return money.Zero(amount.Currency())
	}
	pct, err := amount.PercentBP(f.BP, money.RoundHalfUp)
	if err != nil {
		return money.Money{}, err
	}
	fee := pct.Amount() + f.FixedMinor
	fee = max(fee, f.MinMinor)
	if f.MaxMinor > 0 {
		fee = min(fee, f.MaxMinor)
	}
	return money.New(fee, amount.Currency())
}

func (f CODFee) validate(field string) []errs.FieldError {
	var out []errs.FieldError
	for _, v := range []struct {
		name string
		n    int64
	}{{"fixed_minor", f.FixedMinor}, {"min_minor", f.MinMinor}, {"max_minor", f.MaxMinor}} {
		if v.n < 0 || v.n > MaxAmount {
			out = append(out, errs.FieldError{Field: field + "." + v.name, Code: "range", Message: "must be between 0 and 1000000000"})
		}
	}
	if f.BP < 0 || f.BP > MaxBP {
		out = append(out, errs.FieldError{Field: field + ".bp", Code: "range", Message: fmt.Sprintf("must be between 0 and %d basis points", MaxBP)})
	}
	if f.MaxMinor > 0 && f.MinMinor > f.MaxMinor {
		out = append(out, errs.FieldError{Field: field + ".min_minor", Code: "range", Message: "the minimum is above the maximum"})
	}
	return out
}

// Settings are the store-wide rules; zones may override them.
type Settings struct {
	CODFee CODFee
	// FreeOverMinor: orders whose subtotal reaches it ship free. nil: no
	// free shipping.
	FreeOverMinor *int64
	Version       int // 0: never saved (the defaults)
}

// ValidateSettings checks store-wide settings.
func ValidateSettings(s Settings) error {
	fields := s.CODFee.validate("cod_fee")
	fields = append(fields, validateFreeOver("free_over_minor", s.FreeOverMinor)...)
	if len(fields) > 0 {
		return errs.Validation(fields...)
	}
	return nil
}

func validateFreeOver(field string, v *int64) []errs.FieldError {
	if v != nil && (*v < 0 || *v > MaxAmount) {
		return []errs.FieldError{{Field: field, Code: "range", Message: "must be between 0 and 1000000000"}}
	}
	return nil
}

// CityRate is a city's own rate inside a zone. Key is the city in search
// form (arabic.NormalizeSearch), so "المعادى" and "المعادي" are one city.
type CityRate struct {
	Name      string
	Key       string
	RateMinor int64
}

// CityKey is a city name in the form cities are matched in.
func CityKey(name string) string { return arabic.NormalizeSearch(name) }

// Zone is a set of governorates sharing a rate.
type Zone struct {
	ID            ulid.ID
	Name          string
	Areas         []string // ISO 3166-2 codes, sorted; each in one zone only
	RateMinor     int64
	FreeOverMinor *int64  // overrides Settings.FreeOverMinor
	CODFee        *CODFee // overrides Settings.CODFee
	Cities        []CityRate
	Position      int // display order, from 0
	Version       int
}

// ZoneInput is a zone as a merchant saves it.
type ZoneInput struct {
	Name          string
	Areas         []string
	RateMinor     int64
	FreeOverMinor *int64
	CODFee        *CODFee
	Cities        []CityInput
}

// CityInput is a city's rate as a merchant types it.
type CityInput struct {
	Name      string
	RateMinor int64
}

// ValidateZone cleans and checks a zone of a store in country. Areas are
// deduplicated and sorted; cities are unique by their key.
func ValidateZone(in ZoneInput, country geo.Country) (Zone, error) {
	var fields []errs.FieldError
	name, err := text.Clean(in.Name, "name", 1, MaxZoneNameLen)
	fields = append(fields, fieldsOf(err)...)
	z := Zone{Name: name, RateMinor: in.RateMinor, FreeOverMinor: in.FreeOverMinor, CODFee: in.CODFee}
	if in.RateMinor < 0 || in.RateMinor > MaxAmount {
		fields = append(fields, errs.FieldError{Field: "rate_minor", Code: "range", Message: "must be between 0 and 1000000000"})
	}
	fields = append(fields, validateFreeOver("free_over_minor", in.FreeOverMinor)...)
	if in.CODFee != nil {
		fields = append(fields, in.CODFee.validate("cod_fee")...)
	}

	if len(in.Areas) == 0 {
		fields = append(fields, errs.FieldError{Field: "areas", Code: "required", Message: "a zone has at least one governorate"})
	}
	for i, code := range in.Areas {
		a, ok := geo.Lookup(country, code)
		if !ok {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("areas[%d]", i), Code: "invalid", Message: "not a governorate of the store's country"})
			continue
		}
		z.Areas = append(z.Areas, a.Code)
	}
	slices.Sort(z.Areas)
	z.Areas = slices.Compact(z.Areas)

	if len(in.Cities) > MaxCityRates {
		fields = append(fields, errs.FieldError{Field: "cities", Code: "count", Message: fmt.Sprintf("at most %d cities per zone", MaxCityRates)})
	}
	seen := map[string]bool{}
	for i, c := range in.Cities {
		cname, cerr := text.Clean(c.Name, fmt.Sprintf("cities[%d].name", i), MinCityLen, MaxCityLen)
		fields = append(fields, fieldsOf(cerr)...)
		if c.RateMinor < 0 || c.RateMinor > MaxAmount {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("cities[%d].rate_minor", i), Code: "range", Message: "must be between 0 and 1000000000"})
		}
		if cerr != nil {
			continue
		}
		key := CityKey(cname)
		if key == "" || seen[key] {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("cities[%d].name", i), Code: "duplicate", Message: "this city is already in the zone"})
			continue
		}
		seen[key] = true
		z.Cities = append(z.Cities, CityRate{Name: cname, Key: key, RateMinor: c.RateMinor})
	}
	if len(fields) > 0 {
		return Zone{}, errs.Validation(fields...)
	}
	return z, nil
}

func fieldsOf(err error) []errs.FieldError {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Fields
	}
	return nil
}

// Config is everything a quote needs about one store.
type Config struct {
	Currency money.Currency
	Settings Settings
	Zones    []Zone
}

// QuoteRequest is where an order goes and what it is worth.
type QuoteRequest struct {
	Governorate string
	City        string
	Subtotal    money.Money
}

// Quote is what delivering an order costs.
type Quote struct {
	// Deliverable is false when the store has zones and none holds the
	// governorate.
	Deliverable  bool
	ZoneID       *ulid.ID // nil when the store has no zones
	Shipping     money.Money
	FreeShipping bool // the free-shipping threshold applied
	CODFee       money.Money
}

// Quote prices delivering req:
//   - a store without zones delivers everywhere, shipping free (how
//     stores worked before zones existed), with its COD fee;
//   - otherwise the governorate's zone sets the rate, a city of that zone
//     with its own rate overrides it, and a subtotal at or above the free
//     threshold (the zone's, else the store's) ships free;
//   - the COD fee (the zone's, else the store's) is on the subtotal plus
//     shipping: what the courier collects.
func (c Config) Quote(req QuoteRequest) (Quote, error) {
	if req.Subtotal.Currency() != c.Currency {
		return Quote{}, fmt.Errorf("shipping: subtotal in %s, store in %s", req.Subtotal.Currency(), c.Currency)
	}
	q := Quote{Deliverable: true}
	var err error
	if q.Shipping, err = money.Zero(c.Currency); err != nil {
		return Quote{}, err
	}
	if q.CODFee, err = money.Zero(c.Currency); err != nil {
		return Quote{}, err
	}
	fee, freeOver := c.Settings.CODFee, c.Settings.FreeOverMinor
	if len(c.Zones) > 0 {
		z, ok := c.zoneOf(req.Governorate)
		if !ok {
			return Quote{Deliverable: false, Shipping: q.Shipping, CODFee: q.CODFee}, nil
		}
		q.ZoneID = &z.ID
		rate := z.RateMinor
		if key := CityKey(req.City); key != "" {
			if i := slices.IndexFunc(z.Cities, func(cr CityRate) bool { return cr.Key == key }); i >= 0 {
				rate = z.Cities[i].RateMinor
			}
		}
		if z.FreeOverMinor != nil {
			freeOver = z.FreeOverMinor
		}
		if z.CODFee != nil {
			fee = *z.CODFee
		}
		if freeOver != nil && req.Subtotal.Amount() >= *freeOver {
			q.FreeShipping = true
		} else if q.Shipping, err = money.New(rate, c.Currency); err != nil {
			return Quote{}, err
		}
	} else if freeOver != nil && req.Subtotal.Amount() >= *freeOver {
		q.FreeShipping = true
	}
	collected, err := req.Subtotal.Add(q.Shipping)
	if err != nil {
		return Quote{}, err
	}
	if q.CODFee, err = fee.For(collected); err != nil {
		return Quote{}, err
	}
	return q, nil
}

func (c Config) zoneOf(area string) (Zone, bool) {
	for _, z := range c.Zones {
		if _, found := slices.BinarySearch(z.Areas, area); found {
			return z, true
		}
	}
	return Zone{}, false
}
