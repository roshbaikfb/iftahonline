// Package text cleans short free text typed by people (names, cities,
// addresses, notes, reasons) before it is stored: one line, no control
// or invisible characters, bounded length.
package text

import (
	"fmt"
	"regexp"
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

// Clip cuts s to at most n runes.
func Clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// email matches what looks like an email address.
var email = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`) //nolint:gochecknoglobals // compiled once, read-only

// minRedactedDigits is the shortest digit run Redact hides: phone numbers
// and IDs, not amounts or codes.
const minRedactedDigits = 7

// Redact hides what may identify a person in text written by others
// (a provider's error message, kept in a log or shown to a merchant):
// email addresses become [email], and numbers of 7 or more digits
// (phone numbers, grouped with spaces, dashes or dots, a leading +,
// Arabic-Indic digits too) become [number].
func Redact(s string) string {
	s = email.ReplaceAllString(s, "[email]")
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if !isDigit(rs[i]) {
			b.WriteRune(rs[i])
			i++
			continue
		}
		// A number: digits, with single separators between them.
		j, digits := i, 0
		for j < len(rs) {
			if isDigit(rs[j]) {
				digits++
				j++
				continue
			}
			if isSeparator(rs[j]) && j+1 < len(rs) && isDigit(rs[j+1]) {
				j++
				continue
			}
			break
		}
		if digits >= minRedactedDigits {
			out := strings.TrimSuffix(b.String(), "+")
			b.Reset()
			b.WriteString(out)
			b.WriteString("[number]")
		} else {
			b.WriteString(string(rs[i:j]))
		}
		i = j
	}
	return b.String()
}

func isDigit(r rune) bool { return (r >= '0' && r <= '9') || (r >= '٠' && r <= '٩') }

func isSeparator(r rune) bool { return r == ' ' || r == '-' || r == '.' }
