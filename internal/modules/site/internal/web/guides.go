package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	script "github.com/roshbaikfb/iftahonline/internal/platform/arabic"
)

// The site's guides (S4c): /guides lists them, /guides/{slug} shows one.
// A guide without English is on the Arabic site only.

// guideView is a guide in a page's language.
type guideView struct {
	slug, title, summary, body string // body: Markdown
	cover                      string
	published, updated         time.Time
	minutes                    int
}

// guidesIn are the published guides in l, newest first: English shows
// those written in English.
func guidesIn(l *language, list []domain.Guide) []guideView {
	var out []guideView
	for _, g := range list {
		v := guideView{slug: g.Slug, title: g.TitleAr, summary: g.SummaryAr, body: g.BodyAr, cover: g.CoverKey,
			published: g.PublishedAt, updated: g.UpdatedAt}
		if l == english {
			if !g.HasEnglish() {
				continue
			}
			v.title, v.summary, v.body = g.TitleEn, g.SummaryEn, g.BodyEn
		}
		v.minutes = domain.ReadingMinutes(v.body)
		out = append(out, v)
	}
	return out
}

// hasEnglish says whether the guide at slug has its English version.
func hasEnglish(list []domain.Guide, slug string) bool {
	for _, g := range list {
		if g.Slug == slug {
			return g.HasEnglish()
		}
	}
	return false
}

// coverSize is a guide's cover's width (the article's column, twice).
const coverSize = 1200

func (s *Site) guidesIndex(w http.ResponseWriter, r *http.Request, l *language) {
	p := s.page(r.Context(), l, "/guides", l.t.GuidesTitle, l.t.GuidesDesc)
	if !p.contentOK {
		s.unavailable(w, r, errNoContent)
		return
	}
	ct, _ := s.d.Content.Content(r.Context()) //nolint:errcheck // read above: page says it is ok
	list := guidesIn(l, ct.Guides)
	for i := range list {
		list[i].cover = s.image(list[i].cover, 640, "webp")
	}
	p.jsonLD = []any{s.breadcrumbLD(l, crumb{l.t.Guides, "/guides"})}
	s.write(w, r, http.StatusOK, cachePage, p, guidesPage(p, list))
}

func (s *Site) guide(w http.ResponseWriter, r *http.Request, l *language, slug string) bool {
	ct, err := s.d.Content.Content(r.Context())
	if err != nil {
		s.unavailable(w, r, err)
		return true
	}
	list := guidesIn(l, ct.Guides)
	i := -1
	for j, g := range list {
		if g.slug == slug {
			i = j
		}
	}
	if i < 0 {
		return false
	}
	g := list[i]
	p := s.page(r.Context(), l, "/guides/"+g.slug, g.title+" | "+l.c.brand, g.summary)
	p.arabicOnly = !hasEnglish(ct.Guides, g.slug)
	html, err := s.md.HTML(g.body)
	if err != nil {
		s.unavailable(w, r, err)
		return true
	}
	cover := s.image(g.cover, coverSize, "webp")
	if cover != "" {
		p.share = s.image(g.cover, coverSize, "jpg")
	}
	var more []guideView
	for j, o := range list {
		if j != i && len(more) < 3 {
			more = append(more, o)
		}
	}
	p.jsonLD = []any{s.articleLD(p, g), s.breadcrumbLD(l, crumb{l.t.Guides, "/guides"}, crumb{g.title, p.path})}
	s.write(w, r, http.StatusOK, cachePage, p, guidePage(p, g, cover, templ.Raw(html), more))
	return true
}

// articleLD is a guide as schema.org's Article, by the platform.
func (s *Site) articleLD(p *pageCtx, g guideView) any {
	a := map[string]any{
		"@context": "https://schema.org", "@type": "Article", "headline": g.title, "description": g.summary,
		"inLanguage": p.l.code, "url": p.url(p.l), "mainEntityOfPage": p.url(p.l),
		"datePublished": g.published.UTC().Format(time.RFC3339), "dateModified": g.updated.UTC().Format(time.RFC3339),
		"author":    map[string]any{"@id": s.origin() + "/#organization"},
		"publisher": map[string]any{"@id": s.origin() + "/#organization"},
	}
	if p.share != "" {
		a["image"] = p.share
	}
	return a
}

// guideSitePages are the guides for sitemap.xml and llms.txt, in l.
func (s *Site) guideSitePages(ctx context.Context, l *language) []sitePage {
	ct, err := s.d.Content.Content(ctx)
	if err != nil {
		return nil
	}
	var out []sitePage
	for _, g := range guidesIn(l, ct.Guides) {
		out = append(out, sitePage{"/guides/" + g.slug, g.title, g.summary})
	}
	return out
}

// dateIn is a date as l writes it: "3 نوفمبر 2026", "3 November 2026".
func dateIn(l *language, t time.Time) string {
	if l == english {
		return t.UTC().Format("2 January 2006")
	}
	return script.Date(t.UTC())
}

// minutesIn is a reading time as l says it.
func minutesIn(l *language, n int) string {
	if l == english {
		return strconv.Itoa(n) + " min read"
	}
	switch {
	case n == 1:
		return "قراءة في دقيقة"
	case n == 2:
		return "قراءة في دقيقتين"
	case n <= 10:
		return "قراءة في " + strconv.Itoa(n) + " دقائق"
	default:
		return "قراءة في " + strconv.Itoa(n) + " دقيقة"
	}
}

// isGuide is a guide's path's slug.
func isGuide(path string) (string, bool) {
	slug, ok := strings.CutPrefix(path, "/guides/")
	return slug, ok && slug != "" && !strings.Contains(slug, "/")
}
