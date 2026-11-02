package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// fields are the fields a validation error names.
func fields(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("not a validation error: %v", err)
	}
	var out []string
	for _, f := range e.Fields {
		out = append(out, f.Field)
	}
	return out
}

func TestSettings(t *testing.T) {
	t.Parallel()
	ok := func() domain.Settings {
		return domain.Settings{WhatsApp: "+201153450716", WhatsAppIntl: "+447700900123", Email: "hello@iftahonline.com",
			Socials:       map[domain.Social]string{domain.Facebook: "https://www.facebook.com/iftahonline", domain.X: "https://x.com/iftahonline"},
			CompanyNumber: "12345678"}
	}
	for _, tt := range []struct {
		name  string
		edit  func(*domain.Settings)
		wrong []string
	}{
		{"all set", func(*domain.Settings) {}, nil},
		{"all empty", func(s *domain.Settings) { *s = domain.Settings{} }, nil},
		{"a local number", func(s *domain.Settings) { s.WhatsApp = "01153450716" }, []string{"whatsapp"}},
		{"letters for a number", func(s *domain.Settings) { s.WhatsAppIntl = "+44 call us" }, []string{"whatsapp_intl"}},
		{"no email", func(s *domain.Settings) { s.Email = "hello at iftahonline" }, []string{"email"}},
		{"an email with a name", func(s *domain.Settings) { s.Email = "Iftah <hello@iftahonline.com>" }, []string{"email"}},
		{"http", func(s *domain.Settings) { s.Socials[domain.Instagram] = "http://instagram.com/iftahonline" }, []string{"socials.instagram"}},
		{"another site", func(s *domain.Settings) { s.Socials[domain.TikTok] = "https://tiktok.com.evil.example/x" }, []string{"socials.tiktok"}},
		{"a swapped field", func(s *domain.Settings) { s.Socials[domain.YouTube] = "https://www.facebook.com/iftahonline" }, []string{"socials.youtube"}},
		{"credentials in the link", func(s *domain.Settings) { s.Socials[domain.LinkedIn] = "https://a:b@linkedin.com/company/x" }, []string{"socials.linkedin"}},
		{"an unknown network", func(s *domain.Settings) { s.Socials["myspace"] = "https://myspace.com/x" }, []string{"socials.myspace"}},
		{"a short company number", func(s *domain.Settings) { s.CompanyNumber = "1234" }, []string{"company_number"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := ok()
			tt.edit(&s)
			if got := fields(t, s.Validate()); strings.Join(got, ",") != strings.Join(tt.wrong, ",") {
				t.Fatalf("wrong fields = %v, want %v", got, tt.wrong)
			}
		})
	}
}

func TestSettingsNormalize(t *testing.T) {
	t.Parallel()
	s := domain.Settings{WhatsApp: " +201153450716 ", CompanyNumber: " sc 123456 ",
		Socials: map[domain.Social]string{domain.Facebook: "  ", domain.X: " https://x.com/iftahonline "}}
	s.Normalize()
	if s.WhatsApp != "+201153450716" || s.CompanyNumber != "SC123456" || len(s.Socials) != 1 || s.Socials[domain.X] != "https://x.com/iftahonline" {
		t.Fatalf("normalized = %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := s.CompanyHouseURL(); got != "https://find-and-update.company-information.service.gov.uk/company/SC123456" {
		t.Fatalf("Companies House = %q", got)
	}
	if (domain.Settings{}).CompanyHouseURL() != "" {
		t.Fatal("a record without a number")
	}
}

func TestFAQ(t *testing.T) {
	t.Parallel()
	ok := func() domain.FAQ {
		return domain.FAQ{QuestionAr: "هل يمكنني البدء مجانًا؟", AnswerAr: "نعم، الباقة المجانية دائمة.", QuestionEn: "Can I start free?", AnswerEn: "Yes."}
	}
	for _, tt := range []struct {
		name  string
		edit  func(*domain.FAQ)
		wrong []string
	}{
		{"both languages", func(*domain.FAQ) {}, nil},
		{"Arabic only", func(f *domain.FAQ) { f.QuestionEn, f.AnswerEn = "", "" }, nil},
		{"no Arabic", func(f *domain.FAQ) { f.QuestionAr, f.AnswerAr = "", "" }, []string{"question_ar", "answer_ar"}},
		{"English question without its answer", func(f *domain.FAQ) { f.AnswerEn = "" }, []string{"answer_en"}},
		{"English answer without its question", func(f *domain.FAQ) { f.QuestionEn = "" }, []string{"question_en"}},
		{"a long question", func(f *domain.FAQ) { f.QuestionAr = strings.Repeat("س", 201) }, []string{"question_ar"}},
		{"200 Arabic letters fit", func(f *domain.FAQ) { f.QuestionAr = strings.Repeat("س", 200) }, nil},
		{"a negative position", func(f *domain.FAQ) { f.Position = -1 }, []string{"position"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := ok()
			tt.edit(&f)
			if got := fields(t, f.Validate()); strings.Join(got, ",") != strings.Join(tt.wrong, ",") {
				t.Fatalf("wrong fields = %v, want %v", got, tt.wrong)
			}
		})
	}
}
