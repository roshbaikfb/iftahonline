package arabic_test

import (
	"testing"

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
