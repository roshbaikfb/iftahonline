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

func TestClip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"منى أحمد", 3, "منى"},
		{"abcdef", 0, ""},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := text.Clip(tt.in, tt.n); got != tt.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"phone_number: +201012345678 is not valid", "phone_number: [number] is not valid"},
		{"رقم ٠١٠١٢٣٤٥٦٧٨ غير صحيح", "رقم [number] غير صحيح"},
		{"call 010 1234 5678 or 010-1234-5678", "call [number] or [number]"},
		{"email mona.ahmed@example.com refused", "email [email] refused"},
		{`{"phone":["+201012345678 is not valid"],"email":["mona@mail.example.eg is taken"]}`, `{"phone":["[number] is not valid"],"email":["[email] is taken"]}`},
		{"amount 52000 exceeds 4100", "amount 52000 exceeds 4100"}, // short numbers stay: amounts, codes
		{"Refund amount exceeds the captured amount", "Refund amount exceeds the captured amount"},
	}
	for _, tt := range tests {
		if got := text.Redact(tt.in); got != tt.want {
			t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func FuzzRedact(f *testing.F) {
	for _, s := range []string{"+201012345678", "a@b.co", "٠١٠١٢٣٤٥٦٧٨", "x 1234567 y", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := text.Redact(s)
		digits := 0
		for _, r := range out {
			switch {
			case (r >= '0' && r <= '9') || (r >= '٠' && r <= '٩'):
				digits++
				if digits >= 7 {
					t.Fatalf("Redact(%q) = %q keeps a run of 7 digits", s, out)
				}
			default:
				digits = 0
			}
		}
	})
}
