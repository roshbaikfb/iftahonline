package web_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func TestPrice(t *testing.T) {
	t.Parallel()
	m := func(n int64, c money.Currency) money.Money {
		v, err := money.New(n, c)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		m      money.Money
		ar, en string
	}{
		{m(15000, money.EGP), "150 ج.م", "EGP 150"},
		{m(125050, money.EGP), "1,250.50 ج.م", "EGP 1,250.50"},
		{m(5, money.SAR), "0.05 ر.س", "SAR 0.05"},
		{m(123456789900, money.AED), "1,234,567,899 د.إ", "AED 1,234,567,899"},
		{m(-2500, money.EGP), "-25 ج.م", "EGP -25"},
		{m(0, money.EGP), "0 ج.م", "EGP 0"},
	}
	for _, tt := range tests {
		if got := web.Arabic.Price(tt.m); got != tt.ar {
			t.Errorf("ar %v = %q, want %q", tt.m, got, tt.ar)
		}
		if got := web.English.Price(tt.m); got != tt.en {
			t.Errorf("en %v = %q, want %q", tt.m, got, tt.en)
		}
	}
}

func TestLocales(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]web.Locale{"ar": web.Arabic, "ar-EG": web.Arabic, "en": web.English, "en-AE": web.English, "": web.Arabic, "fr": web.Arabic} {
		if got := web.ParseLocale(in); got != want {
			t.Errorf("ParseLocale(%q) = %s", in, got)
		}
	}
	if web.Arabic.Dir() != "rtl" || web.English.Dir() != "ltr" || web.Arabic.Other() != web.English || web.English.Other() != web.Arabic {
		t.Fatal("direction or other language wrong")
	}
	for _, l := range []web.Locale{web.Arabic, web.English} {
		for m := web.MsgHome; m <= web.MsgBadgeQuality; m++ {
			if l.T(m) == "" {
				t.Errorf("%s: message %d has no text", l, m)
			}
		}
		if l.Or("merchant text", web.MsgHome) != "merchant text" || l.Or("", web.MsgHome) != l.T(web.MsgHome) {
			t.Errorf("%s: Or", l)
		}
		for _, p := range []string{"shipping", "refund", "privacy", "terms"} {
			if l.PolicyTitle(p) == "" {
				t.Errorf("%s: policy %s untitled", l, p)
			}
		}
		if l.BadgeTitle("cod") == "" || l.BadgeTitle("skull") != "" {
			t.Errorf("%s: badge titles", l)
		}
	}
}
