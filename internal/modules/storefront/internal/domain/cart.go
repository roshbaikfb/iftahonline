package domain

import (
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Cart limits (ADR-0019).
const (
	MaxCartLines = 50
	MaxLineQty   = 20
	// CartTTL is how long an untouched cart lives; every change renews it.
	CartTTL = 30 * 24 * time.Hour
)

// CartLine is a variant and its quantity in a cart.
type CartLine struct {
	VariantID ulid.ID
	Qty       int
}

// ValidateQty checks a line quantity; zero is allowed only when removing.
func ValidateQty(qty int, allowZero bool) error {
	lo := 1
	if allowZero {
		lo = 0
	}
	if qty < lo || qty > MaxLineQty {
		return errs.Validation(errs.FieldError{Field: "qty", Code: "range", Message: fmt.Sprintf("quantity must be %d to %d", lo, MaxLineQty)})
	}
	return nil
}
