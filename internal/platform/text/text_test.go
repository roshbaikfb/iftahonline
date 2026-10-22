package text_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

func TestClean(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in       string
		min, max int
		want     string
		code     string // empty: valid
	}{
		{"  أحمد   محمد \n", 0, 120, "أحمد محمد", ""},
		{"Sara\tAli\r\nCairo", 0, 120, "Sara Ali Cairo", ""},
		{"\u200bمنى\ufeff", 0, 120, "منى", ""},
		{"", 0, 10, "", ""},
		{"", 1, 10, "", "required"},
		{" \u200b ", 1, 10, "", "required"},
		{"ab", 3, 10, "", "too_short"},
		{strings.Repeat("ا", 10), 0, 10, strings.Repeat("ا", 10), ""},
		{strings.Repeat("ا", 11), 0, 10, "", "too_long"},
		{"bad \xff", 0, 10, "", "invalid"},
	}
	for _, tt := range tests {
		got, err := text.Clean(tt.in, "f", tt.min, tt.max)
		if tt.code == "" {
			if err != nil || got != tt.want {
				t.Errorf("Clean(%q) = %q, %v", tt.in, got, err)
			}
			continue
		}
		if !errs.Is(err, errs.KindValidation) || !strings.Contains(fieldCodes(err), tt.code) {
			t.Errorf("Clean(%q) err = %v, want %s", tt.in, err, tt.code)
		}
	}
}

func fieldCodes(err error) string {
	var b strings.Builder
	var e *errs.Error
	if !errors.As(err, &e) {
		return ""
	}
	for _, f := range e.Fields {
		b.WriteString(f.Code + " ")
	}
	return b.String()
}

// FuzzClean: the result is always one trimmed line within the limit.
func FuzzClean(f *testing.F) {
	for _, s := range []string{"", "a  b", "\n\t", "منى\u200b", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := text.Clean(s, "f", 0, 50)
		if err != nil {
			return
		}
		if strings.ContainsAny(got, "\n\r\t") || strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") || strings.Contains(got, "  ") {
			t.Fatalf("Clean(%q) = %q", s, got)
		}
	})
}
