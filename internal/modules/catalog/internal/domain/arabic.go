package domain

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
