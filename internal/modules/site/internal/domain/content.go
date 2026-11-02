// Package domain holds the rules of the platform site's content the
// console edits (S4a, ADR-0073): its contact details and its FAQ.
package domain

import (
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Social is a network the site links to.
type Social string

// The networks, in the order the site shows them.
const (
	Facebook  Social = "facebook"
	Instagram Social = "instagram"
	TikTok    Social = "tiktok"
	LinkedIn  Social = "linkedin"
	YouTube   Social = "youtube"
	X         Social = "x"
)

// Socials lists every network, in the site's order.
func Socials() []Social { return []Social{Facebook, Instagram, TikTok, LinkedIn, YouTube, X} }

// hosts are each network's own hosts: a link elsewhere is refused, so a
// typo or a swapped field cannot send visitors to another site.
var hosts = map[Social][]string{ //nolint:gochecknoglobals // constant table
	Facebook:  {"facebook.com", "www.facebook.com", "fb.com"},
	Instagram: {"instagram.com", "www.instagram.com"},
	TikTok:    {"tiktok.com", "www.tiktok.com"},
	LinkedIn:  {"linkedin.com", "www.linkedin.com"},
	YouTube:   {"youtube.com", "www.youtube.com"},
	X:         {"x.com", "twitter.com", "www.x.com", "www.twitter.com"},
}

// Settings are the site's contact details.
type Settings struct {
	// WhatsApp and WhatsAppIntl are E.164 numbers ("+201…"); "" shows none.
	WhatsApp, WhatsAppIntl string
	Email                  string
	// Socials are each network's profile link (https).
	Socials map[Social]string
	// CompanyNumber is Companies House's number of the company.
	CompanyNumber string
}

var (
	companyNumber = regexp.MustCompile(`^[0-9A-Z]{8}$`)
	// e164 is any country's number: the platform's phone package knows
	// only the stores' markets, and the site's numbers may be British.
	e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

// Normalize trims the fields and upper-cases the company number, as the
// console's form sends them.
func (s *Settings) Normalize() {
	s.WhatsApp, s.WhatsAppIntl = strings.TrimSpace(s.WhatsApp), strings.TrimSpace(s.WhatsAppIntl)
	s.Email = strings.TrimSpace(s.Email)
	s.CompanyNumber = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s.CompanyNumber), " ", ""))
	for k, v := range s.Socials {
		if v = strings.TrimSpace(v); v == "" {
			delete(s.Socials, k)
		} else {
			s.Socials[k] = v
		}
	}
}

// Validate checks the settings. Every field may be empty.
func (s Settings) Validate() error {
	var fe []errs.FieldError
	for field, n := range map[string]string{"whatsapp": s.WhatsApp, "whatsapp_intl": s.WhatsAppIntl} {
		if n == "" {
			continue
		}
		if !e164.MatchString(n) {
			fe = append(fe, errs.FieldError{Field: field, Code: "invalid", Message: "an international number: + and the country code"})
		}
	}
	if s.Email != "" {
		if a, err := mail.ParseAddress(s.Email); err != nil || a.Address != s.Email || len(s.Email) > 254 {
			fe = append(fe, errs.FieldError{Field: "email", Code: "invalid", Message: "an email address"})
		}
	}
	for k, link := range s.Socials {
		if !slices.Contains(Socials(), k) {
			fe = append(fe, errs.FieldError{Field: "socials." + string(k), Code: "invalid", Message: "an unknown network"})
			continue
		}
		if !profileLink(k, link) {
			fe = append(fe, errs.FieldError{Field: "socials." + string(k), Code: "invalid", Message: "an https link to the network's own site"})
		}
	}
	if s.CompanyNumber != "" && !companyNumber.MatchString(s.CompanyNumber) {
		fe = append(fe, errs.FieldError{Field: "company_number", Code: "invalid", Message: "8 letters or digits, as Companies House writes it"})
	}
	if len(fe) > 0 {
		slices.SortFunc(fe, func(a, b errs.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return errs.Validation(fe...)
	}
	return nil
}

// profileLink says whether link is an https link on the network's site.
func profileLink(k Social, link string) bool {
	if len(link) > 300 {
		return false
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return slices.Contains(hosts[k], strings.ToLower(u.Hostname()))
}

// CompanyHouseURL is the company's public record ("" without a number).
func (s Settings) CompanyHouseURL() string {
	if s.CompanyNumber == "" {
		return ""
	}
	return "https://find-and-update.company-information.service.gov.uk/company/" + s.CompanyNumber
}

// FAQ is one question of the site, in Arabic and, optionally, English.
type FAQ struct {
	ID       ulid.ID
	Position int
	// OnHome shows it on the home page too (the FAQ page shows all).
	OnHome               bool
	QuestionAr, AnswerAr string
	QuestionEn, AnswerEn string
}

// MaxFAQs bounds the FAQ: the site shows every one on a page.
const MaxFAQs = 100

// Normalize trims the texts.
func (f *FAQ) Normalize() {
	f.QuestionAr, f.AnswerAr = strings.TrimSpace(f.QuestionAr), strings.TrimSpace(f.AnswerAr)
	f.QuestionEn, f.AnswerEn = strings.TrimSpace(f.QuestionEn), strings.TrimSpace(f.AnswerEn)
}

// Validate checks a question: Arabic is required, English goes in pairs.
func (f FAQ) Validate() error {
	var fe []errs.FieldError
	between := func(field, s string, lo, hi int) {
		if n := utf8.RuneCountInString(s); n < lo || n > hi {
			fe = append(fe, errs.FieldError{Field: field, Code: "invalid", Message: "between the lengths allowed"})
		}
	}
	between("question_ar", f.QuestionAr, 3, 200)
	between("answer_ar", f.AnswerAr, 3, 2000)
	if f.QuestionEn != "" || f.AnswerEn != "" {
		between("question_en", f.QuestionEn, 3, 200)
		between("answer_en", f.AnswerEn, 3, 2000)
	}
	if f.Position < 0 || f.Position > 100000 {
		fe = append(fe, errs.FieldError{Field: "position", Code: "invalid", Message: "0 or more"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// Content is what the site shows from the console: the settings and the
// FAQ in order.
type Content struct {
	Settings Settings
	FAQs     []FAQ
}
