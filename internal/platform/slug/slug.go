// Package slug derives and validates URL slugs. Letters of any script are
// kept, because Arabic shops use Arabic slugs. Products, categories and
// storefront pages all follow these rules.
package slug

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// MaxLen is the longest slug, in runes.
const MaxLen = 120

// Make derives a slug from a title. Letters are lower-cased; Arabic
// diacritics and tatweel are dropped and Eastern Arabic digits become
// ASCII; every other run of characters becomes one hyphen. The result may
// be empty (a title of only symbols).
func Make(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	pendingHyphen := false
	for _, r := range s {
		if n >= MaxLen {
			break
		}
		if arabic.IsMark(r) {
			continue
		}
		r = arabic.ASCIIDigit(r)
		if !isSlugRune(r) {
			pendingHyphen = n > 0
			continue
		}
		if pendingHyphen {
			if n+1 >= MaxLen {
				break
			}
			b.WriteByte('-')
			n++
			pendingHyphen = false
		}
		b.WriteRune(unicode.ToLower(r))
		n++
	}
	return b.String()
}

// isSlugRune reports whether r may appear in a slug besides '-'.
func isSlugRune(r rune) bool {
	return (r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')) ||
		(r >= utf8.RuneSelf && unicode.IsLetter(r))
}

// Check validates a normalized slug: 1 to MaxLen runes of lower-case
// letters, ASCII digits and single inner hyphens. The FieldError names
// field.
func Check(field, s string) (errs.FieldError, bool) {
	bad := errs.FieldError{Field: field, Code: "invalid",
		Message: "slug must be 1 to 120 lower-case letters, digits or single hyphens, not starting or ending with a hyphen"}
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxLen ||
		s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return bad, false
	}
	for _, r := range s {
		if r == '-' {
			continue
		}
		if !isSlugRune(r) || unicode.ToLower(r) != r || arabic.IsMark(r) {
			return bad, false
		}
	}
	return errs.FieldError{}, true
}

// Normalize turns user input into a slug candidate: an explicit slug is
// trimmed and lower-cased, an empty one is derived from fallback.
func Normalize(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return Make(fallback)
	}
	return strings.ToLower(s)
}
