package phone_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		def     phone.Region
		want    string
		region  phone.Region
		wantErr error
	}{
		// Egypt
		{"EG national", "01012345678", phone.EG, "+201012345678", phone.EG, nil},
		{"EG no trunk", "1112345678", phone.EG, "+201112345678", phone.EG, nil},
		{"EG plus", "+20 122 345 6789", phone.SA, "+201223456789", phone.EG, nil},
		{"EG 00", "0020 155 123 4567", phone.AE, "+201551234567", phone.EG, nil},
		{"EG plus with trunk", "+20 010 1234 5678", phone.KW, "+201012345678", phone.EG, nil},
		{"EG cc without plus", "201012345678", phone.EG, "+201012345678", phone.EG, nil},
		{"EG arabic digits", "٠١٠١٢٣٤٥٦٧٨", phone.EG, "+201012345678", phone.EG, nil},
		{"EG persian digits", "۰۱۰۱۲۳۴۵۶۷۸", phone.EG, "+201012345678", phone.EG, nil},
		{"EG bidi marks", "\u200e+20\u00a0(10)\u200f-1234.5678", phone.EG, "+201012345678", phone.EG, nil},
		{"EG landline", "0223456789", phone.EG, "", "", phone.ErrInvalid},
		{"EG bad prefix", "01312345678", phone.EG, "", "", phone.ErrInvalid},
		{"EG short", "0101234567", phone.EG, "", "", phone.ErrInvalid},
		{"EG long", "010123456789", phone.EG, "", "", phone.ErrInvalid},
		// Saudi Arabia
		{"SA national", "0501234567", phone.SA, "+966501234567", phone.SA, nil},
		{"SA plus", "+966 55 123 4567", phone.EG, "+966551234567", phone.SA, nil},
		{"SA no trunk", "591234567", phone.SA, "+966591234567", phone.SA, nil},
		{"SA landline", "0112345678", phone.SA, "", "", phone.ErrInvalid},
		{"SA bad prefix", "0511234567", phone.SA, "", "", phone.ErrInvalid},
		// UAE
		{"AE national", "050 123 4567", phone.AE, "+971501234567", phone.AE, nil},
		{"AE plus", "+971-58-123-4567", phone.EG, "+971581234567", phone.AE, nil},
		{"AE bad prefix", "0531234567", phone.AE, "", "", phone.ErrInvalid},
		// Kuwait
		{"KW national", "5123 4567", phone.KW, "+96551234567", phone.KW, nil},
		{"KW plus", "+965 9876 5432", phone.EG, "+96598765432", phone.KW, nil},
		{"KW no trunk allowed", "051234567", phone.KW, "", "", phone.ErrInvalid},
		{"KW landline", "22345678", phone.KW, "", "", phone.ErrInvalid},
		// Qatar
		{"QA national", "3312 3456", phone.QA, "+97433123456", phone.QA, nil},
		{"QA plus", "+974 7012 3456", phone.EG, "+97470123456", phone.QA, nil},
		{"QA landline", "44123456", phone.QA, "", "", phone.ErrInvalid},
		// Bahrain
		{"BH national", "3612 3456", phone.BH, "+97336123456", phone.BH, nil},
		{"BH 66", "+973 6612 3456", phone.EG, "+97366123456", phone.BH, nil},
		{"BH landline", "17123456", phone.BH, "", "", phone.ErrInvalid},
		// Oman
		{"OM national", "9123 4567", phone.OM, "+96891234567", phone.OM, nil},
		{"OM 7", "+968 7123 4567", phone.EG, "+96871234567", phone.OM, nil},
		{"OM landline", "24123456", phone.OM, "", "", phone.ErrInvalid},
		// General
		{"unsupported cc", "+1 415 555 0100", phone.EG, "", "", phone.ErrUnsupportedCountry},
		{"unsupported default", "01012345678", phone.Region("US"), "", "", phone.ErrUnsupportedCountry},
		{"empty", "", phone.EG, "", "", phone.ErrInvalid},
		{"only separators", " - ", phone.EG, "", "", phone.ErrInvalid},
		{"letters", "0101234567a", phone.EG, "", "", phone.ErrInvalid},
		{"extension", "01012345678 ext 1", phone.EG, "", "", phone.ErrInvalid},
		{"plus in middle", "010+12345678", phone.EG, "", "", phone.ErrInvalid},
		{"double plus", "++201012345678", phone.EG, "", "", phone.ErrInvalid},
		{"too long input", strings.Repeat(" ", 60) + "01012345678", phone.EG, "", "", phone.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := phone.Parse(tt.raw, tt.def)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Parse(%q) err = %v, want %v", tt.raw, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Unmasked() != tt.want || got.Region() != tt.region {
				t.Fatalf("Parse(%q) = %s/%s, want %s/%s", tt.raw, got.Unmasked(), got.Region(), tt.want, tt.region)
			}
		})
	}
}

func TestMasking(t *testing.T) {
	t.Parallel()
	tests := []struct{ raw, masked string }{
		{"+201012345678", "+2010******78"},
		{"+966501234567", "+96650*****67"},
		{"+96551234567", "+96551****67"},
	}
	for _, tt := range tests {
		n, err := phone.ParseE164(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		if n.Masked() != tt.masked || n.String() != tt.masked || fmt.Sprintf("%v", n) != tt.masked {
			t.Errorf("%s: Masked=%s String=%s", tt.raw, n.Masked(), n.String())
		}
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "phone", n)
		if strings.Contains(buf.String(), tt.raw) || !strings.Contains(buf.String(), tt.masked) {
			t.Errorf("slog leaked or did not mask: %s", buf.String())
		}
	}
	if (phone.E164{}).Masked() != "" {
		t.Error("zero must mask to empty")
	}
}

func TestParseE164Strict(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "+", "201012345678", "+20 1012345678", "+2010123456789999", "+20101234567a"} {
		if _, err := phone.ParseE164(bad); !errors.Is(err, phone.ErrInvalid) && !errors.Is(err, phone.ErrUnsupportedCountry) {
			t.Errorf("ParseE164(%q) err = %v", bad, err)
		}
	}
}

func TestJSONAndSQL(t *testing.T) {
	t.Parallel()
	n, _ := phone.Parse("01012345678", phone.EG)
	b, err := json.Marshal(struct{ P phone.E164 }{n})
	if err != nil || string(b) != `{"P":"+201012345678"}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var back struct{ P phone.E164 }
	if uerr := json.Unmarshal(b, &back); uerr != nil || back.P != n {
		t.Fatalf("unmarshal = %v, %v", back.P, uerr)
	}
	if json.Unmarshal([]byte(`{"P":"01012345678"}`), &back) == nil {
		t.Fatal("non-canonical JSON must be rejected")
	}

	v, err := n.Value()
	if err != nil || v != "+201012345678" {
		t.Fatalf("Value = %v, %v", v, err)
	}
	for _, src := range []any{"+201012345678", []byte("+201012345678")} {
		var s phone.E164
		if err := s.Scan(src); err != nil || s != n {
			t.Fatalf("Scan(%T) = %v, %v", src, s, err)
		}
	}
	var s phone.E164
	if err := s.Scan(nil); err != nil || !s.IsZero() {
		t.Fatalf("Scan(nil) = %v, %v", s, err)
	}
	if v, _ := s.Value(); v != nil {
		t.Fatalf("zero Value = %v", v)
	}
	if err := s.Scan(7); err == nil {
		t.Fatal("Scan(int) must fail")
	}
}

func TestRegions(t *testing.T) {
	t.Parallel()
	if got := len(phone.Regions()); got != 7 {
		t.Fatalf("Regions = %d, want 7", got)
	}
}
