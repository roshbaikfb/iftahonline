package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits (ADR-0021).
const (
	MaxLines        = 50
	MaxQty          = 100
	MinNameLen      = 2
	MaxNameLen      = 120
	MinCityLen      = 2
	MaxCityLen      = 80
	MinAddressLen   = 5
	MaxAddressLen   = 300
	MaxNotesLen     = 500
	MaxKeyLen       = 100
	DuplicateWindow = 30 * time.Minute
)

// ErrNotFound is returned by the store when no row matches.
var ErrNotFound = errors.New("orders: not found")

// Source is where an order was placed.
type Source string

// Sources.
const (
	SourceStorefront Source = "storefront" // the shopper's one-step form
	SourceMerchant   Source = "merchant"   // entered by staff (a phone order)
)

// Contact is who receives the order and where. The phone is in full: the
// merchant calls it and the courier needs it; it is never logged.
type Contact struct {
	Name        string
	Phone       phone.E164
	Governorate string // ISO 3166-2 (platform/geo)
	City        string
	Address     string
	Notes       string
}

// LineInput asks for Qty units of a variant.
type LineInput struct {
	VariantID ulid.ID
	Qty       int
}

// NewOrder is an order as submitted, before validation.
type NewOrder struct {
	// IdempotencyKey makes a retried submit return the first order
	// instead of placing another. Optional.
	IdempotencyKey string
	Source         Source
	Name           string
	Phone          string // any accepted format; read in the store's country
	Governorate    string
	City           string
	Address        string
	Notes          string
	Lines          []LineInput
}

// Draft is a validated NewOrder.
type Draft struct {
	IdempotencyKey string
	Source         Source
	Contact        Contact
	Lines          []LineInput // sorted by variant, no repeats
	// Fingerprint identifies the request: the same key with another
	// fingerprint is a reused key, not a retry.
	Fingerprint [32]byte
	// LinesHash identifies what was ordered (variants and quantities), for
	// the duplicate-order window.
	LinesHash [32]byte
}

// ValidateNewOrder cleans and checks in for a store in country.
func ValidateNewOrder(in NewOrder, country geo.Country) (Draft, error) {
	var fields []errs.FieldError
	add := func(err error) {
		var e *errs.Error
		if errors.As(err, &e) {
			fields = append(fields, e.Fields...)
		}
	}
	d := Draft{Source: in.Source}
	if in.Source != SourceStorefront && in.Source != SourceMerchant {
		fields = append(fields, errs.FieldError{Field: "source", Code: "invalid", Message: "unknown order source"})
	}
	key, err := text.Clean(in.IdempotencyKey, "idempotency_key", 0, MaxKeyLen)
	add(err)
	if err == nil && (key != in.IdempotencyKey || strings.ContainsRune(key, ' ')) {
		fields = append(fields, errs.FieldError{Field: "idempotency_key", Code: "invalid", Message: "the idempotency key must be printable with no spaces"})
	}
	d.IdempotencyKey = key

	c := &d.Contact
	c.Name, err = text.Clean(in.Name, "name", MinNameLen, MaxNameLen)
	add(err)
	if c.Phone, err = phone.Parse(in.Phone, phone.Region(country)); err != nil {
		fields = append(fields, errs.FieldError{Field: "phone", Code: "invalid", Message: "enter a valid mobile number"})
	}
	if a, ok := geo.Lookup(country, in.Governorate); ok {
		c.Governorate = a.Code
	} else {
		fields = append(fields, errs.FieldError{Field: "governorate", Code: "invalid", Message: "choose a governorate from the list"})
	}
	c.City, err = text.Clean(in.City, "city", MinCityLen, MaxCityLen)
	add(err)
	c.Address, err = text.Clean(in.Address, "address", MinAddressLen, MaxAddressLen)
	add(err)
	c.Notes, err = text.Clean(in.Notes, "notes", 0, MaxNotesLen)
	add(err)

	lines, lineErrs := validateLines(in.Lines)
	fields = append(fields, lineErrs...)
	if len(fields) > 0 {
		return Draft{}, errs.Validation(fields...)
	}
	d.Lines = lines
	d.LinesHash = hashLines(lines)
	d.Fingerprint = fingerprint(d)
	return d, nil
}

// validateLines checks an order's lines and returns them sorted by
// variant.
func validateLines(in []LineInput) ([]LineInput, []errs.FieldError) {
	var fields []errs.FieldError
	if len(in) == 0 || len(in) > MaxLines {
		fields = append(fields, errs.FieldError{Field: "lines", Code: "count", Message: fmt.Sprintf("an order has 1 to %d lines", MaxLines)})
	}
	lines := slices.Clone(in)
	slices.SortFunc(lines, func(a, b LineInput) int { return bytes.Compare(a.VariantID[:], b.VariantID[:]) })
	for i, l := range lines {
		if l.VariantID.IsZero() {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("lines[%d].variant_id", i), Code: "required", Message: "variant_id is required"})
		}
		if l.Qty < 1 || l.Qty > MaxQty {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("lines[%d].qty", i), Code: "range", Message: fmt.Sprintf("qty must be between 1 and %d", MaxQty)})
		}
		if i > 0 && lines[i-1].VariantID == l.VariantID {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("lines[%d].variant_id", i), Code: "duplicate", Message: "each variant may appear once"})
		}
	}
	return lines, fields
}

// QuoteInput asks what an order would cost before it is placed: the
// storefront shows it while the shopper fills the form.
type QuoteInput struct {
	Governorate string
	City        string // may be empty: then the zone's rate applies
	Lines       []LineInput
}

// ValidateQuote cleans and checks in for a store in country.
func ValidateQuote(in QuoteInput, country geo.Country) (QuoteInput, error) {
	var fields []errs.FieldError
	out := QuoteInput{}
	if a, ok := geo.Lookup(country, in.Governorate); ok {
		out.Governorate = a.Code
	} else {
		fields = append(fields, errs.FieldError{Field: "governorate", Code: "invalid", Message: "choose a governorate from the list"})
	}
	city, err := text.Clean(in.City, "city", 0, MaxCityLen)
	var e *errs.Error
	if errors.As(err, &e) {
		fields = append(fields, e.Fields...)
	}
	out.City = city
	var lineErrs []errs.FieldError
	out.Lines, lineErrs = validateLines(in.Lines)
	fields = append(fields, lineErrs...)
	if len(fields) > 0 {
		return QuoteInput{}, errs.Validation(fields...)
	}
	return out, nil
}

func hashLines(lines []LineInput) [32]byte {
	h := sha256.New()
	var q [8]byte
	for _, l := range lines {
		h.Write(l.VariantID[:])
		binary.BigEndian.PutUint64(q[:], uint64(l.Qty)) //nolint:gosec // 1..MaxQty
		h.Write(q[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// fingerprint hashes everything the submitter chose, with each field
// length-prefixed so no two requests collide by moving a separator.
func fingerprint(d Draft) [32]byte {
	h := sha256.New()
	var n [8]byte
	for _, s := range []string{string(d.Source), d.Contact.Name, d.Contact.Phone.Unmasked(), d.Contact.Governorate,
		d.Contact.City, d.Contact.Address, d.Contact.Notes} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	h.Write(d.LinesHash[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Order is a placed order.
type Order struct {
	ID              ulid.ID
	Status          Status
	Version         int
	Source          Source
	CustomerID      ulid.ID
	Contact         Contact
	Subtotal        money.Money
	Shipping        money.Money // the delivery rate quoted when placed (ADR-0025)
	CODFee          money.Money // the cash-on-delivery fee quoted when placed
	Total           money.Money // Subtotal + Shipping + CODFee
	ItemCount       int
	Lines           []Line // empty in lists
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StatusChangedAt time.Time
}

// Code is the short reference shown to merchants and shoppers: the
// ULID's last 8 characters (random, never sequential; CLAUDE.md rule 6).
func (o Order) Code() string { return CodeOf(o.ID) }

// CodeOf is Order.Code for an ID.
func CodeOf(id ulid.ID) string {
	s := id.String()
	return s[len(s)-8:]
}

// CreatedAtOf is the creation time an order ID encodes. Orders are
// partitioned by it, so a lookup by ID reaches one partition.
func CreatedAtOf(id ulid.ID) time.Time { return id.Time().UTC() }

// Line is one ordered variant, priced when the order was placed. Titles
// are copied so the order reads the same after the catalog changes.
type Line struct {
	VariantID    ulid.ID
	ProductID    ulid.ID
	Title        string
	VariantTitle string // option values, e.g. "M / Red"
	SKU          string
	Qty          int
	UnitPrice    money.Money
	Total        money.Money
}

// Event is one entry of an order's timeline.
type Event struct {
	ID          ulid.ID
	From        Status // empty when placed
	To          Status
	ActorUserID *ulid.ID // nil for the shopper or the system
	Actor       string   // the system actor, e.g. "carrier:bosta"; empty for people
	Note        string
	At          time.Time
}
