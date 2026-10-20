package domain

import (
	"strings"
	"unicode"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
)

// foldArabic maps letter variants that shoppers type interchangeably to
// one form, so "أحذية", "احذيه" and "إحذية" match.
func foldArabic(r rune) rune {
	switch r {
	case 'أ', 'إ', 'آ', 'ٱ', 'ٲ', 'ٳ': // alef with hamza, madda or wasla
		return 'ا'
	case 'ى', 'ی', 'ې': // alef maqsura, Farsi and Pashto ya
		return 'ي'
	case 'ة': // ta marbuta
		return 'ه'
	case 'ک': // Farsi kaf
		return 'ك'
	case 'ؤ':
		return 'و'
	case 'ئ':
		return 'ي'
	}
	return r
}

// NormalizeSearch is the search form of text (ADR-0013). It is applied to
// indexed documents and to queries alike:
//   - lower case;
//   - Arabic diacritics and tatweel dropped;
//   - alef forms (أ إ آ ٱ) → ا, ى → ي, ة → ه, ؤ → و, ئ → ي, Farsi ی/ک;
//   - Arabic-Indic digits → ASCII;
//   - control characters and runs of spaces → one space, trimmed.
func NormalizeSearch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case arabic.IsMark(r) || r == 0x200C || r == 0x200D || r == 0xFEFF:
			continue // marks, zero-width joiners and BOMs say nothing
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(foldArabic(arabic.ASCIIDigit(unicode.ToLower(r))))
	}
	return b.String()
}
