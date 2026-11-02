package web

import (
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
)

// The about page's people, merchants' reviews and the video (S4b-1), in
// the page's language.

// personView is a founder or a team member as a page shows them.
type personView struct {
	name, role, bio string
	photo, initials string
	linkedIn, x     string
}

// reviewView is a merchant's review as a page shows it.
type reviewView struct {
	name, store, storeURL, quote string
	photo, initials              string
}

// photoSize is the side the photos are fitted into (twice the largest
// box, for sharp screens).
const photoSize = 320

// peopleIn are the founders and the team in l: English names fall back
// to the Arabic, an English role or bio left empty is left out.
func (s *Site) peopleIn(l *language, list []domain.Person) (founders, team []personView) {
	for _, p := range list {
		v := personView{name: p.NameAr, role: p.RoleAr, bio: p.BioAr, linkedIn: p.LinkedIn, x: p.X, photo: s.image(p.PhotoKey, photoSize, "webp")}
		if l == english {
			v.role, v.bio = p.RoleEn, p.BioEn
			if p.NameEn != "" {
				v.name = p.NameEn
			}
		}
		v.initials = initials(v.name)
		if p.Kind == domain.Founder {
			founders = append(founders, v)
		} else {
			team = append(team, v)
		}
	}
	return founders, team
}

// reviewsIn are the reviews in l: English shows only those written in
// English.
func (s *Site) reviewsIn(l *language, list []domain.Testimonial) []reviewView {
	var out []reviewView
	for _, t := range list {
		quote := t.QuoteAr
		if l == english {
			if quote = t.QuoteEn; quote == "" {
				continue
			}
		}
		out = append(out, reviewView{name: t.Name, store: t.StoreName, storeURL: t.StoreURL, quote: quote,
			photo: s.image(t.PhotoKey, photoSize, "webp"), initials: initials(t.Name)})
	}
	return out
}

// image is a console image's signed URL, or "".
func (s *Site) image(key string, size int, format string) string {
	if key == "" || s.d.Images == nil {
		return ""
	}
	return s.d.Images(key, size, format)
}

// initials are a name's first letters (two words at most), for a person
// without a photo.
func initials(name string) string {
	var b strings.Builder
	for i, w := range strings.Fields(name) {
		if i == 2 {
			break
		}
		r, _ := utf8.DecodeRuneInString(w)
		b.WriteRune(r)
	}
	return b.String()
}

// videoLink opens the video on YouTube; videoThumb is its thumbnail.
func videoLink(id string) string  { return "https://www.youtube.com/watch?v=" + id }
func videoThumb(id string) string { return youTubeThumbs + "/vi/" + id + "/hqdefault.jpg" }
