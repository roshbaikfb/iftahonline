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

// PaymentMethod is how the shopper pays (ADR-0029).
type PaymentMethod string

// Payment methods.
const (
	PaymentCOD  PaymentMethod = "cod"  // cash on delivery
	PaymentCard PaymentMethod = "card" // online, on the gateway's hosted page
)

// PaymentStatus is where the order's money is.
type PaymentStatus string

// Payment statuses. A COD order is unpaid until delivered (the carrier
// collects); a card order awaits the gateway until paid.
const (
	PaymentUnpaid            PaymentStatus = "unpaid"
	PaymentAwaiting          PaymentStatus = "awaiting"
	PaymentPaid              PaymentStatus = "paid"
	PaymentPartiallyRefunded PaymentStatus = "partially_refunded"
	PaymentRefunded          PaymentStatus = "refunded"
)

// Valid reports whether s is a payment status.
func (s PaymentStatus) Valid() bool {
	switch s {
	case PaymentUnpaid, PaymentAwaiting, PaymentPaid, PaymentPartiallyRefunded, PaymentRefunded:
		return true
	}
	return false
}

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
	// PaymentMethod is cod (the default) or card; card orders come from
	// the storefront only.
	PaymentMethod PaymentMethod
	Name          string
	Phone         string // any accepted format; read in the store's country
	Governorate   string
	City          string
	Address       string
	Notes         string
	Lines         []LineInput
	// CouponCode is a coupon to apply (promotions decides whether it
	// does). Optional.
	CouponCode string
	// Attribution and Signals come from the shopper's browser
	// (ADR-0043); both are optional and cleaned, never refused.
	Attribution Attribution
	Signals     Signals
}

// Draft is a validated NewOrder.
type Draft struct {
	IdempotencyKey string
	Source         Source
	PaymentMethod  PaymentMethod
	Contact        Contact
	Lines          []LineInput // sorted by variant, no repeats
	CouponCode     string      // upper case, "" for none
	Attribution    Attribution // cleaned; not part of the fingerprint
	Signals        Signals     // cleaned; not part of the fingerprint
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
	d := Draft{Source: in.Source, PaymentMethod: in.PaymentMethod}
	if in.Source != SourceStorefront && in.Source != SourceMerchant {
		fields = append(fields, errs.FieldError{Field: "source", Code: "invalid", Message: "unknown order source"})
	}
	switch {
	case d.PaymentMethod == "":
		d.PaymentMethod = PaymentCOD
	case d.PaymentMethod == PaymentCard && in.Source == SourceMerchant:
		fields = append(fields, errs.FieldError{Field: "payment_method", Code: "invalid", Message: "staff orders are cash on delivery"})
	case d.PaymentMethod != PaymentCOD && d.PaymentMethod != PaymentCard:
		fields = append(fields, errs.FieldError{Field: "payment_method", Code: "invalid", Message: "pay cash on delivery or by card"})
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
	if d.CouponCode, err = CleanCouponCode(in.CouponCode); err != nil {
		add(err)
	}
	d.Attribution, d.Signals = in.Attribution.Clean(), in.Signals.Clean()

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

// MaxCouponCodeLen bounds the coupon code a shopper may type.
const MaxCouponCodeLen = 32

// CleanCouponCode trims and upper-cases a coupon code ("" is none);
// whether it names a coupon is promotions' question.
func CleanCouponCode(s string) (string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) > MaxCouponCodeLen || strings.ContainsAny(s, " \t\r\n") {
		return "", errs.Validation(errs.FieldError{Field: "coupon_code", Code: "not_found", Message: "this code does not exist"})
	}
	return s, nil
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
	Governorate   string
	City          string // may be empty: then the zone's rate applies
	Lines         []LineInput
	PaymentMethod PaymentMethod // cod (default) or card: card pays no COD fee
	CouponCode    string        // optional
}

// ValidateQuote cleans and checks in for a store in country.
func ValidateQuote(in QuoteInput, country geo.Country) (QuoteInput, error) {
	var fields []errs.FieldError
	out := QuoteInput{PaymentMethod: in.PaymentMethod}
	if out.PaymentMethod == "" {
		out.PaymentMethod = PaymentCOD
	}
	if out.PaymentMethod != PaymentCOD && out.PaymentMethod != PaymentCard {
		fields = append(fields, errs.FieldError{Field: "payment_method", Code: "invalid", Message: "pay cash on delivery or by card"})
	}
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
	if out.CouponCode, err = CleanCouponCode(in.CouponCode); errors.As(err, &e) {
		fields = append(fields, e.Fields...)
	}
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
	if d.PaymentMethod != PaymentCOD { // COD fingerprints stay as they were before payment methods
		h.Write([]byte("payment:" + string(d.PaymentMethod)))
	}
	if d.CouponCode != "" { // fingerprints without a coupon stay as they were
		h.Write([]byte("coupon:" + d.CouponCode))
	}
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
	CODFee          money.Money // the cash-on-delivery fee quoted when placed (0 for card orders)
	Discount        money.Money // the coupon's, off the items (ADR-0042)
	CouponCode      string      // the coupon the order took, "" for none
	Attribution     Attribution // the campaign that brought it (ADR-0043)
	Total           money.Money // Subtotal - Discount + Shipping + CODFee
	PaymentMethod   PaymentMethod
	PaymentStatus   PaymentStatus
	ItemCount       int
	Lines           []Line // empty in lists
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StatusChangedAt time.Time
	// OutcomeReason is why it did not reach the customer, from its latest
	// move (ADR-0065); ReasonNone otherwise.
	OutcomeReason OutcomeReason
	// RiskAction and RiskBand are what the store's risk rules did to it
	// when it was placed, and the customer's band then (ADR-0066).
	RiskAction RiskAction
	RiskBand   string
	// Deposit is a cash order's prepaid shipping (ADR-0067): shipping and
	// COD fee, paid online; zero for other orders. DepositStatus follows
	// the gateway ("" without a deposit).
	Deposit       money.Money
	DepositStatus PaymentStatus
}

// OnlineDue is what the shopper still has to pay online, and whether
// anything is due: a card order's total, or a cash order's deposit,
// while awaiting the gateway.
func (o Order) OnlineDue() (money.Money, bool) {
	switch {
	case o.PaymentMethod == PaymentCard && o.PaymentStatus == PaymentAwaiting:
		return o.Total, true
	case o.PaymentMethod == PaymentCOD && o.DepositStatus == PaymentAwaiting:
		return o.Deposit, true
	}
	return money.Money{}, false
}

// OnDelivery is what the courier collects: a cash order's total, less a
// deposit that was paid; nothing for a card order.
func (o Order) OnDelivery() (money.Money, error) {
	switch {
	case o.PaymentMethod == PaymentCard:
		return money.Zero(o.Total.Currency())
	case o.DepositStatus == PaymentPaid && !o.Deposit.IsZero():
		return o.Total.Sub(o.Deposit)
	}
	return o.Total, nil
}

// Code is the short reference shown to merchants and shoppers: the
// ULID's last 8 characters (random, never sequential; CLAUDE.md rule 6).
func (o Order) Code() string { return CodeOf(o.ID) }

// CodeOf is Order.Code for an ID.
func CodeOf(id ulid.ID) string {
	s := id.String()
	return s[len(s)-8:]
}

// crockford is the alphabet of ULID strings, so of order codes.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NormalizeCode reads an order code as a shopper types it: spaces and a
// leading "#" dropped, any case. ok is false unless eight characters of
// the alphabet remain.
func NormalizeCode(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimPrefix(strings.Join(strings.Fields(s), ""), "#"))
	if len(s) != 8 || strings.Trim(s, crockford) != "" {
		return "", false
	}
	return s, true
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
	// UnitCost is what the variant cost the merchant when the order was
	// placed (ADR-0068); nil when it was not known.
	UnitCost *money.Money
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
