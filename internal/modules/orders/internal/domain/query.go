package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// List limits.
const (
	DefaultListLimit = 25
	MaxListLimit     = 100
)

// Query selects orders, newest first.
type Query struct {
	Status      *Status
	From, To    *time.Time // created at, [From, To)
	Governorate string
	Phone       string // any accepted format
	CustomerID  *ulid.ID
	Before      *ulid.ID // cursor: the last order of the previous page
	Limit       int
}

// Filter is a validated Query, as the store applies it.
//
// The store drives the walk by the most selective index: customer, then
// phone, then status, then the primary key; the other filters apply
// inside that walk.
type Filter struct {
	Status      *Status
	From, To    time.Time // always set; To is exclusive
	Governorate string
	Phone       *phone.E164
	CustomerID  *ulid.ID
	Before      ulid.ID // always a bound (the largest ID on the first page)
	Limit       int
}

// MaxID is larger than every ULID; the first page's keyset bound.
var MaxID = ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff} //nolint:gochecknoglobals // a constant value; Go has no array constants

// ResolveQuery validates q for a store in country. The created-at range
// is always bounded (all of time by default) so every partition is
// pruned or walked by index the same way.
func ResolveQuery(q Query, country geo.Country) (Filter, error) {
	var fields []errs.FieldError
	f := Filter{Status: q.Status, CustomerID: q.CustomerID, Before: MaxID, Limit: q.Limit,
		From: time.Unix(0, 0).UTC(), To: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)}
	switch {
	case f.Limit == 0:
		f.Limit = DefaultListLimit
	case f.Limit < 1 || f.Limit > MaxListLimit:
		fields = append(fields, errs.FieldError{Field: "limit", Code: "range", Message: fmt.Sprintf("limit must be between 1 and %d", MaxListLimit)})
	}
	if q.From != nil {
		f.From = q.From.UTC()
	}
	if q.To != nil {
		f.To = q.To.UTC()
	}
	if !f.From.Before(f.To) {
		fields = append(fields, errs.FieldError{Field: "to", Code: "range", Message: "to must be after from"})
	}
	if g := strings.TrimSpace(q.Governorate); g != "" {
		a, ok := geo.Lookup(country, g)
		if !ok {
			fields = append(fields, errs.FieldError{Field: "governorate", Code: "invalid", Message: "unknown governorate"})
		}
		f.Governorate = a.Code
	}
	if p := strings.TrimSpace(q.Phone); p != "" {
		e, err := phone.Parse(p, phone.Region(country))
		if err != nil {
			fields = append(fields, errs.FieldError{Field: "phone", Code: "invalid", Message: "not a supported mobile number"})
		}
		f.Phone = &e
	}
	if q.Before != nil {
		f.Before = *q.Before
		if t := CreatedAtOf(*q.Before); t.Before(f.To) {
			f.To = t.Add(time.Millisecond) // the cursor's own millisecond may hold older IDs
		}
	}
	if len(fields) > 0 {
		return Filter{}, errs.Validation(fields...)
	}
	return f, nil
}
