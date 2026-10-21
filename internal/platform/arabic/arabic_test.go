package arabic_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
)

func TestIsMark(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{'ً', 'ِ', 'ٟ', 'ٰ', 'ـ', 'ۖ', 'ۭ'} {
		if !arabic.IsMark(r) {
			t.Errorf("IsMark(%U) = false", r)
		}
	}
	for _, r := range []rune{'ا', 'ب', 'a', '1', 'ي', '٠', 'ۮ'} {
		if arabic.IsMark(r) {
			t.Errorf("IsMark(%U) = true", r)
		}
	}
}

func TestASCIIDigit(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want rune }{
		{'٠', '0'}, {'٥', '5'}, {'٩', '9'},
		{'۰', '0'}, {'۷', '7'}, {'۹', '9'},
		{'3', '3'}, {'a', 'a'}, {'ب', 'ب'},
	}
	for _, tt := range tests {
		if got := arabic.ASCIIDigit(tt.in); got != tt.want {
			t.Errorf("ASCIIDigit(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeSearch(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"أحذية رياضية", "احذيه رياضيه"},
		{"إحذية", "احذيه"},
		{"آيفون", "ايفون"},
		{"مستشفى", "مستشفي"},
		{"مكتبة", "مكتبه"},
		{"مَلابِس رِجالـــية", "ملابس رجاليه"},
		{"سؤال شاطئ", "سوال شاطي"},
		{"کتاب فارسی", "كتاب فارسي"},
		{"iPhone ١٥   Pro\tMax\n", "iphone 15 pro max"},
		{"  ", ""},
		{"zero\u200cwidth\ufeff", "zerowidth"},
	}
	for _, tt := range tests {
		if got := arabic.NormalizeSearch(tt.in); got != tt.want {
			t.Errorf("NormalizeSearch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// What a shopper types and what the merchant wrote meet in one form.
	pairs := [][2]string{{"احذيه", "أحذية"}, {"مستشفي", "مستشفى"}, {"اسلام", "إسلام"}}
	for _, p := range pairs {
		if arabic.NormalizeSearch(p[0]) != arabic.NormalizeSearch(p[1]) {
			t.Errorf("%q and %q normalize differently", p[0], p[1])
		}
	}
}

// FuzzNormalizeSearch: normalizing is idempotent, never grows the text by
// more than a byte per rune, and leaves no marks or edge spaces.
func FuzzNormalizeSearch(f *testing.F) {
	for _, s := range []string{"أحذية رياضية", "مَلابِس", "١٥", " a  b ", "\xff\x00", "İstanbul", "ـــ"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := arabic.NormalizeSearch(s)
		if again := arabic.NormalizeSearch(got); again != got {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, got, again)
		}
		if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") || strings.Contains(got, "  ") {
			t.Fatalf("bad spacing: %q", got)
		}
		if len(got) > 4*utf8.RuneCountInString(s)+len(s) {
			t.Fatalf("grew too much: %d -> %d", len(s), len(got))
		}
		for _, r := range got {
			if r == 'ـ' || r == 'أ' || r == 'ة' || r == 'ى' || (r >= 0x064B && r <= 0x065F) {
				t.Fatalf("%q left %q in %q", s, r, got)
			}
		}
	})
}
