// Package text cleans short free text typed by people (names, cities,
// addresses, notes, reasons) before it is stored: one line, no control
// or invisible characters, bounded length.
package text

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Clean trims s, folds runs of spaces, line breaks and control or
// zero-width characters to one space, and checks its length in runes.
// field names the input in the validation error.
func Clean(s, field string, minRunes, maxRunes int) (string, error) {
	if !utf8.ValidString(s) {
		return "", errs.Validation(errs.FieldError{Field: field, Code: "invalid", Message: field + " must be valid UTF-8"})
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == 0x200B || r == 0xFEFF {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	switch n := utf8.RuneCountInString(out); {
	case n > maxRunes:
		return "", errs.Validation(errs.FieldError{Field: field, Code: "too_long", Message: fmt.Sprintf("%s must be at most %d characters", field, maxRunes)})
	case n < minRunes && n == 0:
		return "", errs.Validation(errs.FieldError{Field: field, Code: "required", Message: field + " is required"})
	case n < minRunes:
		return "", errs.Validation(errs.FieldError{Field: field, Code: "too_short", Message: fmt.Sprintf("%s must be at least %d characters", field, minRunes)})
	}
	return out, nil
}
