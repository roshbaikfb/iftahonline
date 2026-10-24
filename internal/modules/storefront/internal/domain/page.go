package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/slug"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits (ADR-0016). They bound request size, render cost and what one
// page can hold.
const (
	MaxSections           = 30
	MaxSectionsBytes      = 64 << 10 // all of a page's sections as JSON
	MaxLandingPages       = 50
	MaxPageTitleLen       = 200
	MaxPageDescriptionLen = 300
	MaxPolicyBodyLen      = 20000
	MaxSectionIDLen       = 32
)

// PageKind says what a page is for.
type PageKind string

// Page kinds. A store has one home page, up to MaxLandingPages landing
// pages and one page per policy.
const (
	KindHome    PageKind = "home"
	KindLanding PageKind = "landing"
	KindPolicy  PageKind = "policy"
)

// HomeSlug is the home page's fixed slug.
const HomeSlug = "home"

// Policy slugs: the policies a store can publish.
const (
	PolicyPrivacy  = "privacy"
	PolicyRefund   = "refund"
	PolicyShipping = "shipping"
	PolicyTerms    = "terms"
)

// PolicySlugs lists every policy, in display order.
func PolicySlugs() []string {
	return []string{PolicyShipping, PolicyRefund, PolicyPrivacy, PolicyTerms}
}

// PageStatus says whether shoppers can see a page.
type PageStatus string

// Page statuses.
const (
	StatusDraft     PageStatus = "draft"
	StatusPublished PageStatus = "published"
)

// SectionType names a section component.
type SectionType string

// Section types (ADR-0016). Each has a JSON schema for its settings and a
// templ component.
const (
	SectionHero           SectionType = "hero"
	SectionProductGrid    SectionType = "product_grid"
	SectionProductLanding SectionType = "product_landing"
	SectionReviews        SectionType = "reviews"
	SectionFAQ            SectionType = "faq"
	SectionTrustBadges    SectionType = "trust_badges"
	SectionCountdown      SectionType = "countdown"
	SectionCategoryTiles  SectionType = "category_tiles"
	SectionPromoTiles     SectionType = "promo_tiles"
	SectionWhatsAppCTA    SectionType = "whatsapp_cta"
)

// SectionTypes lists every section type.
func SectionTypes() []SectionType {
	return []SectionType{
		SectionHero, SectionProductGrid, SectionProductLanding, SectionReviews, SectionFAQ, SectionTrustBadges, SectionCountdown,
		SectionCategoryTiles, SectionPromoTiles, SectionWhatsAppCTA,
	}
}

// Section is one block of a page. Settings are validated against the
// type's JSON schema before they are stored.
type Section struct {
	ID       string          `json:"id"`
	Type     SectionType     `json:"type"`
	Settings json.RawMessage `json:"settings"`
}

// Page is a home, landing or policy page.
type Page struct {
	ID          ulid.ID
	Kind        PageKind
	Slug        string
	Title       string
	Description string // meta description for search engines
	Sections    []Section
	Body        string // policies only
	Status      PageStatus
	Version     int // 0: not saved yet (a default)
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PageInput creates or replaces a page. Version is the version the
// caller read: 0 to create.
type PageInput struct {
	Slug        string
	Title       string
	Description string
	Sections    []Section
	Body        string
	Status      string
	Version     int
}

// SettingsValidator checks a section's settings against its type's
// schema. Field names in the result are relative to the settings object
// ("items[0].name").
type SettingsValidator interface {
	ValidateSettings(t SectionType, raw json.RawMessage) []errs.FieldError
}

// ValidatePage normalizes in for a page of kind (policies: slug is the
// policy) and checks every rule, reporting all invalid fields at once.
// Sections without an ID get one from newID.
func ValidatePage(kind PageKind, policy string, in PageInput, sv SettingsValidator, newID func() string) (PageInput, error) {
	out := PageInput{
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Status:      strings.TrimSpace(in.Status),
		Body:        strings.TrimSpace(in.Body),
		Version:     in.Version,
	}
	var fields []errs.FieldError
	add := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}
	if fe, ok := checkLine("title", out.Title, MaxPageTitleLen, kind != KindLanding); !ok {
		fields = append(fields, fe)
	}
	if fe, ok := checkLine("description", out.Description, MaxPageDescriptionLen, true); !ok {
		fields = append(fields, fe)
	}
	if in.Version < 0 {
		add("version", "range", "version must be 0 or more")
	}

	switch kind {
	case KindHome:
		out.Slug, out.Status = HomeSlug, string(StatusPublished)
	case KindPolicy:
		if !slices.Contains(PolicySlugs(), policy) {
			add("policy", "invalid", "unknown policy")
		}
		out.Slug = policy
	case KindLanding:
		out.Slug = slug.Normalize(in.Slug, out.Title)
		if out.Slug == "" {
			add("slug", "required", "slug is required when the title has no letters or digits")
		} else if fe, ok := slug.Check("slug", out.Slug); !ok {
			fields = append(fields, fe)
		}
	default:
		add("kind", "invalid", "unknown page kind")
	}
	if kind != KindHome {
		if out.Status == "" {
			out.Status = string(StatusDraft)
		}
		if out.Status != string(StatusDraft) && out.Status != string(StatusPublished) {
			add("status", "invalid", "status must be draft or published")
		}
	}

	if kind == KindPolicy {
		if len(in.Sections) > 0 {
			add("sections", "not_allowed", "policies have a body, not sections")
		}
		if utf8.RuneCountInString(out.Body) > MaxPolicyBodyLen || !utf8.ValidString(out.Body) || hasControl(out.Body, "\n\t") {
			add("body", "length", fmt.Sprintf("body must be at most %d characters of text", MaxPolicyBodyLen))
		}
		out.Sections = []Section{}
	} else {
		if out.Body != "" {
			add("body", "not_allowed", "only policies have a body")
		}
		var secFields []errs.FieldError
		out.Sections, secFields = validateSections(in.Sections, sv, newID)
		fields = append(fields, secFields...)
	}

	if len(fields) > 0 {
		return PageInput{}, errs.Validation(fields...)
	}
	return out, nil
}

func validateSections(in []Section, sv SettingsValidator, newID func() string) ([]Section, []errs.FieldError) {
	var fields []errs.FieldError
	if len(in) > MaxSections {
		return nil, []errs.FieldError{{Field: "sections", Code: "too_many", Message: fmt.Sprintf("a page can have at most %d sections", MaxSections)}}
	}
	out := make([]Section, len(in))
	seen := make(map[string]bool, len(in))
	for i, s := range in {
		at := "sections[" + strconv.Itoa(i) + "]"
		s.ID = strings.ToLower(strings.TrimSpace(s.ID))
		if s.ID == "" {
			s.ID = newID()
		}
		switch {
		case !validSectionID(s.ID):
			fields = append(fields, errs.FieldError{Field: at + ".id", Code: "invalid", Message: "id must be 1 to 32 lower-case letters, digits or hyphens"})
		case seen[s.ID]:
			fields = append(fields, errs.FieldError{Field: at + ".id", Code: "duplicate", Message: "section IDs must be unique on a page"})
		}
		seen[s.ID] = true
		if len(s.Settings) == 0 || string(s.Settings) == "null" {
			s.Settings = json.RawMessage("{}")
		}
		if !slices.Contains(SectionTypes(), s.Type) {
			fields = append(fields, errs.FieldError{Field: at + ".type", Code: "invalid", Message: "unknown section type"})
		} else {
			for _, fe := range sv.ValidateSettings(s.Type, s.Settings) {
				fe.Field = joinField(at+".settings", fe.Field)
				fields = append(fields, fe)
			}
		}
		out[i] = s
	}
	if len(fields) == 0 {
		if b, err := json.Marshal(out); err != nil || len(b) > MaxSectionsBytes {
			fields = append(fields, errs.FieldError{Field: "sections", Code: "too_large", Message: fmt.Sprintf("sections must be at most %d KiB of JSON", MaxSectionsBytes>>10)})
		}
	}
	return out, fields
}

func joinField(prefix, field string) string {
	switch {
	case field == "":
		return prefix
	case strings.HasPrefix(field, "["):
		return prefix + field
	}
	return prefix + "." + field
}

func validSectionID(id string) bool {
	if id == "" || len(id) > MaxSectionIDLen {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// checkLine validates a single-line display string of at most maxLen
// runes.
func checkLine(field, s string, maxLen int, allowEmpty bool) (errs.FieldError, bool) {
	n := utf8.RuneCountInString(s)
	if (n == 0 && !allowEmpty) || n > maxLen || !utf8.ValidString(s) {
		return errs.FieldError{Field: field, Code: "length", Message: fmt.Sprintf("%s must be 1 to %d characters", field, maxLen)}, false
	}
	if hasControl(s, "") {
		return errs.FieldError{Field: field, Code: "invalid", Message: field + " must not contain control characters"}, false
	}
	return errs.FieldError{}, true
}

// hasControl reports whether s has a control character not in allowed.
func hasControl(s, allowed string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && !strings.ContainsRune(allowed, r) {
			return true
		}
	}
	return false
}
