package money

import (
	"strconv"
	"strings"
)

// Locale selects digits, separators and symbol placement for Format.
type Locale int

// Supported locales.
const (
	// LocaleEN renders "EGP 1,234.50".
	LocaleEN Locale = iota
	// LocaleAR renders "١٬٢٣٤٫٥٠ ج.م." with Arabic-Indic digits.
	LocaleAR
)

const (
	arDecimal   = '٫' // U+066B ARABIC DECIMAL SEPARATOR
	arThousands = '٬' // U+066C ARABIC THOUSANDS SEPARATOR
	arZero      = '٠' // U+0660; digits are contiguous through U+0669
	faZero      = '۰' // U+06F0; extended (Persian/Urdu) digits through U+06F9
)

// String renders the English format. It is safe to log.
func (m Money) String() string { return m.Format(LocaleEN) }

// Format renders m for display. An invalid Money renders as its raw parts.
func (m Money) Format(l Locale) string {
	info, ok := m.currency.info()
	if !ok {
		return strconv.FormatInt(m.amount, 10) + " " + string(m.currency)
	}
	num := m.formatNumber(info, l)
	var b strings.Builder
	b.Grow(len(num) + 12)
	if m.amount < 0 {
		b.WriteByte('-')
	}
	if l == LocaleAR {
		b.WriteString(num)
		b.WriteByte(' ')
		b.WriteString(info.symbolAR)
		return b.String()
	}
	b.WriteString(string(m.currency))
	b.WriteByte(' ')
	b.WriteString(num)
	return b.String()
}

// FormatAmount renders the signed number without a currency symbol,
// e.g. "-1,234.50". Parse accepts its output.
func (m Money) FormatAmount(l Locale) string {
	info, ok := m.currency.info()
	if !ok {
		return strconv.FormatInt(m.amount, 10)
	}
	num := m.formatNumber(info, l)
	if m.amount < 0 {
		return "-" + num
	}
	return num
}

// formatNumber renders |amount| with grouping and the decimal separator.
func (m Money) formatNumber(info currencyInfo, l Locale) string {
	mag := absU(m.amount)
	whole := strconv.FormatUint(mag/info.scale, 10)
	frac := strconv.FormatUint(mag%info.scale, 10)

	thousands, decimal := ',', '.'
	if l == LocaleAR {
		thousands, decimal = arThousands, arDecimal
	}

	var b strings.Builder
	b.Grow(2 * (len(whole) + info.minor + 8))
	for i, d := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteRune(thousands)
		}
		writeDigit(&b, d, l)
	}
	if info.minor > 0 {
		b.WriteRune(decimal)
		for range info.minor - len(frac) {
			writeDigit(&b, '0', l)
		}
		for _, d := range []byte(frac) {
			writeDigit(&b, d, l)
		}
	}
	return b.String()
}

func writeDigit(b *strings.Builder, d byte, l Locale) {
	if l == LocaleAR {
		b.WriteRune(arZero + rune(d-'0'))
		return
	}
	b.WriteByte(d)
}

// asciiDigit maps ASCII, Arabic-Indic and extended Arabic-Indic digits to
// their value.
func asciiDigit(r rune) (uint64, bool) {
	switch {
	case r >= '0' && r <= '9':
		return uint64(r - '0'), true
	case r >= arZero && r <= arZero+9:
		return uint64(r - arZero), true
	case r >= faZero && r <= faZero+9:
		return uint64(r - faZero), true
	default:
		return 0, false
	}
}

// Decimal writes m as a plain decimal in its currency's digits ("1250.50",
// "-0.05"): no grouping, no symbol, for machines (structured data,
// spreadsheets, feeds, ad platforms).
func (m Money) Decimal() string { return Decimal(m.amount, m.currency.MinorUnits()) }

// Decimal writes minor units as a plain decimal with units digits after
// the point.
func Decimal(minor int64, units int) string {
	s := strconv.FormatInt(minor, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if units > 0 {
		if len(s) <= units {
			s = strings.Repeat("0", units-len(s)+1) + s
		}
		s = s[:len(s)-units] + "." + s[len(s)-units:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
