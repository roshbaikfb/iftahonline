package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The site's guides (S4c): articles written in Markdown, Arabic first,
// English optional, published at /guides/{slug}.

// GuideStatus is whether a guide is on the site.
type GuideStatus string

// The statuses.
const (
	Draft     GuideStatus = "draft"
	Published GuideStatus = "published"
)

// Guide is one article.
type Guide struct {
	ID     ulid.ID
	Slug   string // its address: /guides/{slug}
	Status GuideStatus
	// The Arabic version is required; the English is optional, as one.
	TitleAr, SummaryAr, BodyAr string
	TitleEn, SummaryEn, BodyEn string
	CoverKey                   string // "" shows none
	PublishedAt                time.Time
	UpdatedAt                  time.Time
}

// MaxGuides bounds the guides (the site keeps the published in memory).
const MaxGuides = 200

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Normalize trims the texts and lowers the slug.
func (g *Guide) Normalize() {
	g.Slug = strings.ToLower(strings.TrimSpace(g.Slug))
	for _, s := range []*string{&g.TitleAr, &g.SummaryAr, &g.BodyAr, &g.TitleEn, &g.SummaryEn, &g.BodyEn} {
		*s = strings.TrimSpace(*s)
	}
}

// Validate checks a guide.
func (g Guide) Validate() error {
	fe := lengths(
		field{"title_ar", g.TitleAr, 5, 120}, field{"summary_ar", g.SummaryAr, 20, 300}, field{"body_ar", g.BodyAr, 50, 40000},
	)
	if g.HasEnglish() || g.SummaryEn != "" || g.BodyEn != "" {
		fe = append(fe, lengths(field{"title_en", g.TitleEn, 5, 120}, field{"summary_en", g.SummaryEn, 20, 300}, field{"body_en", g.BodyEn, 50, 40000})...)
	}
	if !slugPattern.MatchString(g.Slug) || len(g.Slug) > 80 {
		fe = append(fe, errs.FieldError{Field: "slug", Code: "invalid", Message: "lowercase English letters, digits and dashes (dash-between-words)"})
	}
	if g.Status != Draft && g.Status != Published {
		fe = append(fe, errs.FieldError{Field: "status", Code: "invalid", Message: "draft or published"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// HasEnglish says whether the guide has its English version.
func (g Guide) HasEnglish() bool { return g.TitleEn != "" }

// Check normalizes, then validates.
func (g *Guide) Check() error { g.Normalize(); return g.Validate() }

// Keys are where the guide's ID and cover are kept.
func (g *Guide) Keys() (id *ulid.ID, cover *string) { return &g.ID, &g.CoverKey }

// Publish sets when a guide is first published: kept when it was
// published before (unpublishing and publishing again keeps its date).
func (g *Guide) Publish(had time.Time, now time.Time) {
	switch {
	case g.Status != Published:
		g.PublishedAt = had
	case !had.IsZero():
		g.PublishedAt = had
	default:
		g.PublishedAt = now
	}
}

// ReadingMinutes is how long a text takes to read, at 200 words a
// minute, at least one.
func ReadingMinutes(text string) int {
	words := len(strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) }))
	return max(1, (words+199)/200)
}

// Excerpt is a text cut to n characters at a word, with "…".
func Excerpt(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	cut := string([]rune(text)[:n])
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}
