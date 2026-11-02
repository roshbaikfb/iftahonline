package domain

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The about page's content and the site's identity (S4b-1).

// Story is the founding story and the site's video.
type Story struct {
	Ar, En string
	// VideoID is a YouTube video's ID; "" shows none.
	VideoID string
}

// MaxStory bounds the story, in characters.
const MaxStory = 4000

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ParseVideo reads a YouTube link (watch, youtu.be, shorts, embed) or a
// bare ID into the video's ID. "" is no video.
func ParseVideo(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", nil
	}
	if videoID.MatchString(link) {
		return link, nil
	}
	bad := errs.Validation(errs.FieldError{Field: "video_url", Code: "invalid", Message: "a YouTube video's link"})
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", bad
	}
	var id string
	switch strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") {
	case "youtube.com", "m.youtube.com":
		switch {
		case u.Path == "/watch":
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/shorts/"), strings.HasPrefix(u.Path, "/embed/"), strings.HasPrefix(u.Path, "/live/"):
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			id = parts[len(parts)-1]
		}
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	}
	if !videoID.MatchString(id) {
		return "", bad
	}
	return id, nil
}

// Normalize trims the story.
func (s *Story) Normalize() {
	s.Ar, s.En = strings.TrimSpace(s.Ar), strings.TrimSpace(s.En)
}

// Validate checks the story's length and the video's ID.
func (s Story) Validate() error {
	var fe []errs.FieldError
	if utf8.RuneCountInString(s.Ar) > MaxStory {
		fe = append(fe, errs.FieldError{Field: "story_ar", Code: "invalid", Message: "at most 4000 characters"})
	}
	if utf8.RuneCountInString(s.En) > MaxStory {
		fe = append(fe, errs.FieldError{Field: "story_en", Code: "invalid", Message: "at most 4000 characters"})
	}
	if s.VideoID != "" && !videoID.MatchString(s.VideoID) {
		fe = append(fe, errs.FieldError{Field: "video_url", Code: "invalid", Message: "a YouTube video's link"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// Paragraphs are a text's paragraphs: blank lines split them.
func Paragraphs(text string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Image is one of the site's own images.
type Image string

// The site's images.
const (
	Logo       Image = "logo"
	ShareImage Image = "share" // what a shared link shows (Open Graph)
)

// ImageKey is where a re-encoded image of id is kept.
func ImageKey(id ulid.ID, format string) string { return "platform/site/" + id.String() + "." + format }

// MaxImageBytes bounds an uploaded image.
const MaxImageBytes = 4 << 20

// PersonKind is a founder or a team member.
type PersonKind string

// The kinds.
const (
	Founder PersonKind = "founder"
	Team    PersonKind = "team"
)

// Person is a founder or a team member on the about page.
type Person struct {
	ID       ulid.ID
	Kind     PersonKind
	Position int
	// NameEn, RoleEn and BioEn are the English site's; "" falls back to
	// the Arabic name and leaves the role or bio out.
	NameAr, NameEn string
	RoleAr, RoleEn string
	BioAr, BioEn   string
	PhotoKey       string // "" shows the initials
	LinkedIn, X    string // profile links; "" shows none
}

// MaxPeople bounds the founders and the team together.
const MaxPeople = 50

// Normalize trims the texts.
func (p *Person) Normalize() {
	for _, s := range []*string{&p.NameAr, &p.NameEn, &p.RoleAr, &p.RoleEn, &p.BioAr, &p.BioEn, &p.LinkedIn, &p.X} {
		*s = strings.TrimSpace(*s)
	}
}

// Validate checks a person.
func (p Person) Validate() error {
	fe := lengths(
		field{"name_ar", p.NameAr, 2, 80}, field{"name_en", p.NameEn, 0, 80},
		field{"role_ar", p.RoleAr, 2, 80}, field{"role_en", p.RoleEn, 0, 80},
		field{"bio_ar", p.BioAr, 0, 600}, field{"bio_en", p.BioEn, 0, 600},
	)
	if p.Kind != Founder && p.Kind != Team {
		fe = append(fe, errs.FieldError{Field: "kind", Code: "invalid", Message: "founder or team"})
	}
	if p.LinkedIn != "" && !profileLink(LinkedIn, p.LinkedIn) {
		fe = append(fe, errs.FieldError{Field: "linkedin_url", Code: "invalid", Message: "an https LinkedIn link"})
	}
	if p.X != "" && !profileLink(X, p.X) {
		fe = append(fe, errs.FieldError{Field: "x_url", Code: "invalid", Message: "an https X link"})
	}
	if p.Position < 0 || p.Position > 100000 {
		fe = append(fe, errs.FieldError{Field: "position", Code: "invalid", Message: "0 or more"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// Testimonial is a merchant's own words about the platform. The console
// adds only real ones (the owner's decision, and the UK's rules on fake
// reviews); the site shows none until one exists.
type Testimonial struct {
	ID       ulid.ID
	Position int
	// Name is the merchant's, StoreName and StoreURL their store's.
	Name, StoreName, StoreURL string
	// QuoteEn is the English site's; "" leaves the review off it.
	QuoteAr, QuoteEn string
	PhotoKey         string
}

// MaxTestimonials bounds the reviews.
const MaxTestimonials = 50

// Normalize trims the texts.
func (t *Testimonial) Normalize() {
	for _, s := range []*string{&t.Name, &t.StoreName, &t.StoreURL, &t.QuoteAr, &t.QuoteEn} {
		*s = strings.TrimSpace(*s)
	}
}

// Validate checks a review.
func (t Testimonial) Validate() error {
	fe := lengths(
		field{"name", t.Name, 2, 80}, field{"store_name", t.StoreName, 0, 80},
		field{"quote_ar", t.QuoteAr, 10, 600}, field{"quote_en", t.QuoteEn, 0, 600},
	)
	if t.QuoteEn != "" && utf8.RuneCountInString(t.QuoteEn) < 10 {
		fe = append(fe, errs.FieldError{Field: "quote_en", Code: "invalid", Message: "between the lengths allowed"})
	}
	if t.StoreURL != "" {
		if u, err := url.Parse(t.StoreURL); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(t.StoreURL) > 300 {
			fe = append(fe, errs.FieldError{Field: "store_url", Code: "invalid", Message: "an https link"})
		}
	}
	if t.Position < 0 || t.Position > 100000 {
		fe = append(fe, errs.FieldError{Field: "position", Code: "invalid", Message: "0 or more"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// field is a text and the lengths it may have.
type field struct {
	name   string
	text   string
	lo, hi int
}

// lengths are the fields whose text is not between their lengths (an
// optional field, lo 0, may be empty).
func lengths(fields ...field) []errs.FieldError {
	var fe []errs.FieldError
	for _, f := range fields {
		if n := utf8.RuneCountInString(f.text); n < f.lo || n > f.hi {
			fe = append(fe, errs.FieldError{Field: f.name, Code: "invalid", Message: "between the lengths allowed"})
		}
	}
	return fe
}
