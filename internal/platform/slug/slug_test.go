package slug_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/slug"
)

func TestMake(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"Red T-Shirt", "red-t-shirt"},
		{"  Hello,   World!  ", "hello-world"},
		{"قميص أحمر", "قميص-أحمر"},
		{"مَلابِس رِجالـــية", "ملابس-رجالية"}, // diacritics and tatweel dropped
		{"iPhone ١٥ Pro", "iphone-15-pro"},     // Arabic-Indic digits
		{"ساعة ۱۲", "ساعة-12"},                 // Eastern Arabic-Indic digits
		{"!!!", ""},
		{"", ""},
		{"a--b__c", "a-b-c"},
		{strings.Repeat("a", 130), strings.Repeat("a", slug.MaxLen)},
		{strings.Repeat("ab ", 60), strings.TrimSuffix(strings.Repeat("ab-", 40), "-")},
	}
	for _, tt := range tests {
		if got := slug.Make(tt.in); got != tt.want {
			t.Errorf("Make(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// FuzzMake: whatever a merchant types, a non-empty derived slug is a
// valid slug, and deriving is idempotent.
func FuzzMake(f *testing.F) {
	for _, s := range []string{"Red T-Shirt", "قميص أحمر", "مَلابِس", "١٥", "a--b", "\x00\xff", "ǅ", "İstanbul"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := slug.Make(s)
		if got == "" {
			return
		}
		if _, ok := slug.Check("slug", got); !ok {
			t.Fatalf("Make(%q) = %q, which Check rejects", s, got)
		}
		if again := slug.Make(got); again != got {
			t.Fatalf("Make not idempotent: %q -> %q -> %q", s, got, again)
		}
		if utf8.RuneCountInString(got) > slug.MaxLen {
			t.Fatalf("slug too long: %d runes", utf8.RuneCountInString(got))
		}
	})
}

func TestCheck(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"a", "red-shirt", "قميص-أحمر", "x1"} {
		if _, valid := slug.Check("slug", ok); !valid {
			t.Errorf("Check(%q) rejected", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "a--b", "Red", "a b", "a/b", "مَ", strings.Repeat("a", 121), "\xff"} {
		fe, valid := slug.Check("page_slug", bad)
		if valid || fe.Field != "page_slug" || fe.Code != "invalid" {
			t.Errorf("Check(%q) = %+v, %v", bad, fe, valid)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, fallback, want string }{
		{"  My-Slug ", "ignored", "my-slug"},
		{"", "عرض الصيف", "عرض-الصيف"},
		{"   ", "Summer Sale", "summer-sale"},
	}
	for _, tt := range tests {
		if got := slug.Normalize(tt.in, tt.fallback); got != tt.want {
			t.Errorf("Normalize(%q, %q) = %q, want %q", tt.in, tt.fallback, got, tt.want)
		}
	}
}
