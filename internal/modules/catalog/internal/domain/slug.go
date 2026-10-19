package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// MaxSlugLen is the longest slug, in runes.
const MaxSlugLen = 120

// Slugify derives a URL slug from a title. Letters of any script are kept
// (Arabic shops use Arabic slugs), lower-cased; Arabic diacritics and
// tatweel are dropped and Eastern Arabic digits become ASCII; every other
// run of characters becomes one hyphen. The result may be empty (a title
// of only symbols).
func Slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	pendingHyphen := false
	for _, r := range s {
		if n >= MaxSlugLen {
			break
		}
		if isArabicMark(r) {
			continue
		}
		r = asciiDigit(r)
		if !isSlugRune(r) {
			pendingHyphen = n > 0
			continue
		}
		if pendingHyphen {
			if n+1 >= MaxSlugLen {
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

// CheckSlug validates a normalized slug: 1 to MaxSlugLen runes of
// lower-case letters, ASCII digits and single inner hyphens.
func CheckSlug(field, s string) (errs.FieldError, bool) {
	bad := errs.FieldError{Field: field, Code: "invalid",
		Message: "slug must be 1 to 120 lower-case letters, digits or single hyphens, not starting or ending with a hyphen"}
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxSlugLen ||
		s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return bad, false
	}
	for _, r := range s {
		if r == '-' {
			continue
		}
		if !isSlugRune(r) || unicode.ToLower(r) != r || isArabicMark(r) {
			return bad, false
		}
	}
	return errs.FieldError{}, true
}

// normalizeSlug turns user input into a slug candidate: an explicit slug
// is trimmed and lower-cased, an empty one is derived from fallback.
func normalizeSlug(slug, fallback string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return Slugify(fallback)
	}
	return strings.ToLower(slug)
}
