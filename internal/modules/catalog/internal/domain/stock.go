package domain

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Reservation limits (ADR-0011).
const (
	DefaultReservationTTL = 15 * time.Minute
	MaxReservationTTL     = 24 * time.Hour
	MaxReservationLines   = 50
	MaxReserveQty         = 10_000
	MaxRefLen             = 100
)

// ReservationStatus is a reservation line's state.
type ReservationStatus string

// Reservation statuses.
const (
	ReservationActive   ReservationStatus = "active"
	ReservationReleased ReservationStatus = "released"
	ReservationExpired  ReservationStatus = "expired"
	// ReservationCommitted: the units left the warehouse (the order
	// shipped); on_hand dropped with reserved.
	ReservationCommitted ReservationStatus = "committed"
	// ReservationReturned: committed units came back into stock.
	ReservationReturned ReservationStatus = "returned"
)

// ReserveLine asks for Qty units of one variant.
type ReserveLine struct {
	VariantID ulid.ID
	Qty       int
}

// ReserveRequest holds stock for a checkout or order until it is released
// or expires. Ref is the caller's idempotency key: reserving the same ref
// again returns the existing reservation.
type ReserveRequest struct {
	Ref   string
	Lines []ReserveLine
	// TTL is how long the stock is held; zero means DefaultReservationTTL.
	TTL time.Duration
	// Hold keeps the stock until it is released or committed, with no
	// expiry: an order waiting for the merchant (ADR-0020). TTL must be
	// zero.
	Hold bool
}

// Reservation is the stock held for one ref.
type Reservation struct {
	Ref    string
	Lines  []ReserveLine
	Status ReservationStatus
	// Held reservations have no expiry (ReserveRequest.Hold).
	Held bool
	// ExpiresAt is zero when Held.
	ExpiresAt time.Time
}

// SameLines reports whether r holds exactly the lines of req (both
// sorted by variant, as ValidateReserve leaves them), held the same way.
func (r Reservation) SameLines(req ReserveRequest) bool {
	return slices.Equal(r.Lines, req.Lines) && r.Held == req.Hold
}

// SellableVariant is what checkout needs to sell a variant: price, stock
// and the product it belongs to. Only variants of active, live products
// are sellable.
type SellableVariant struct {
	VariantID    ulid.ID
	ProductID    ulid.ID
	ProductTitle string
	ProductSlug  string
	SKU          string
	OptionValues []string
	Price        money.Money
	CompareAt    *money.Money
	Stock        Stock
}

// ValidateReserve checks a reservation request and returns it with lines
// sorted by variant ID: reserving in one global order means two
// concurrent multi-line reservations can never deadlock.
func ValidateReserve(in ReserveRequest) (ReserveRequest, error) {
	var fields []errs.FieldError
	add := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}
	if !validRef(in.Ref) {
		add("ref", "invalid", fmt.Sprintf("ref must be 1 to %d characters of A-Z a-z 0-9 . _ : -", MaxRefLen))
	}
	switch {
	case in.Hold && in.TTL != 0:
		add("ttl", "conflict", "a held reservation has no ttl")
	case in.Hold:
	case in.TTL == 0:
		in.TTL = DefaultReservationTTL
	case in.TTL < time.Minute || in.TTL > MaxReservationTTL:
		add("ttl", "range", "ttl must be between 1 minute and 24 hours")
	}
	if len(in.Lines) == 0 || len(in.Lines) > MaxReservationLines {
		add("lines", "count", fmt.Sprintf("a reservation needs 1 to %d lines", MaxReservationLines))
	}
	lines := slices.Clone(in.Lines)
	slices.SortFunc(lines, func(a, b ReserveLine) int { return bytes.Compare(a.VariantID[:], b.VariantID[:]) })
	for i, l := range lines {
		if l.VariantID.IsZero() {
			add(fmt.Sprintf("lines[%d].variant_id", i), "required", "variant_id is required")
		}
		if l.Qty < 1 || l.Qty > MaxReserveQty {
			add(fmt.Sprintf("lines[%d].qty", i), "range", fmt.Sprintf("qty must be between 1 and %d", MaxReserveQty))
		}
		if i > 0 && lines[i-1].VariantID == l.VariantID {
			add(fmt.Sprintf("lines[%d].variant_id", i), "duplicate", "each variant may appear once")
		}
	}
	if len(fields) > 0 {
		return ReserveRequest{}, errs.Validation(fields...)
	}
	in.Lines = lines
	return in, nil
}

func validRef(s string) bool {
	if s == "" || len(s) > MaxRefLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != ':' && c != '-' {
			return false
		}
	}
	return true
}

// ValidateRef checks a reservation ref on its own (for release).
func ValidateRef(ref string) error {
	if !validRef(ref) {
		return errs.Validation(errs.FieldError{Field: "ref", Code: "invalid", Message: "invalid reservation ref"})
	}
	return nil
}

// StockInput sets a variant's stock.
type StockInput struct {
	Tracked bool
	OnHand  int
}

// ValidateStock checks a stock update.
func ValidateStock(in StockInput) error {
	if in.OnHand < 0 || in.OnHand > MaxOnHand {
		return errs.Validation(errs.FieldError{Field: "on_hand", Code: "range", Message: fmt.Sprintf("on_hand must be between 0 and %d", MaxOnHand)})
	}
	return nil
}
