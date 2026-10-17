package money_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Format and Parse run on the storefront path (price rendering, cart input).

func BenchmarkFormat(b *testing.B) {
	m, _ := money.New(123456789, money.EGP)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.Format(money.LocaleEN)
	}
}

func BenchmarkFormatAR(b *testing.B) {
	m, _ := money.New(123456789, money.EGP)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.Format(money.LocaleAR)
	}
}

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = money.Parse("1,234,567.89", money.EGP)
	}
}
