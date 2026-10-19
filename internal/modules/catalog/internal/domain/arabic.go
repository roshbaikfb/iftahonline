package domain

import (
	"strings"
	"unicode"
)

// isArabicMark reports whether r is an Arabic diacritic (harakat, tanwin,
// shadda, sukun, superscript alef, Quranic marks) or the tatweel, which
// change how text looks but not what it says.
func isArabicMark(r rune) bool {
	return (r >= 0x064B && r <= 0x065F) || r == 0x0670 || r == 0x0640 ||
		(r >= 0x06D6 && r <= 0x06ED)
}

// asciiDigit maps Arabic-Indic (U+0660..U+0669) and Eastern Arabic-Indic
// (U+06F0..U+06F9) digits to ASCII; every other rune is returned as is.
func asciiDigit(r rune) rune {
	switch {
	case r >= 0x0660 && r <= 0x0669:
		return '0' + (r - 0x0660)
	case r >= 0x06F0 && r <= 0x06F9:
		return '0' + (r - 0x06F0)
	}
	return r
}

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
		case isArabicMark(r) || r == 0x200C || r == 0x200D || r == 0xFEFF:
			continue // marks, zero-width joiners and BOMs say nothing
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(foldArabic(asciiDigit(unicode.ToLower(r))))
	}
	return b.String()
}
