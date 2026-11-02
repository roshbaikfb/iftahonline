// Package web renders the platform's site (ADR-0071): its pages, its
// schema.org data and the files crawlers read.
package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed" // the stylesheet
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
)

//go:embed assets/dist/site.css
var siteCSS string

// Content is the content the console edits, kept a minute in memory
// (site/internal/app).
type Content interface {
	Content(ctx context.Context) (domain.Content, error)
}

// Deps are the site's dependencies.
type Deps struct {
	Plans   Plans
	Content Content
	// FontFaces and FontPreload are the brand typeface the shop serves.
	FontFaces   string
	FontPreload []string
	// Host is the platform's domain the site answers on (iftahonline.com);
	// HTTPS says whether its links are https.
	Host  string
	HTTPS bool
	// DashboardURL is where merchants sign up and sign in.
	DashboardURL string
	// WhatsApp is the support number in E.164 (+20…) shown while the
	// console's settings cannot be read.
	WhatsApp string
	// CountryHeader names the CDN's visitor country header.
	CountryHeader string
	Log           *slog.Logger
	Now           func() time.Time
}

// Site serves the platform's website.
type Site struct {
	d     Deps
	plans *planCache
	css   string // the inline stylesheet: the brand faces, then site.css
	csp   string
}

// New builds the site.
func New(d Deps) (*Site, error) {
	if d.Plans == nil || d.Content == nil || d.Host == "" || d.DashboardURL == "" || d.Log == nil {
		return nil, errors.New("site: missing dependency")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	css := d.FontFaces + strings.TrimSpace(siteCSS)
	sum := sha256.Sum256([]byte(css))
	csp := "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; font-src 'self'; img-src 'self' data:; " +
		"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	return &Site{d: d, plans: &planCache{src: d.Plans, now: d.Now, got: map[string]cachedOffers{}}, css: css, csp: csp}, nil
}

// Cache lifetimes: pages change when the copy, the console's content or
// the plans do (the edge keeps them 10 minutes, approved for S4); the
// pricing page depends on the visitor's country, so the edge never keeps
// it.
const (
	cachePage    = "public, max-age=300, s-maxage=600"
	cachePrivate = "private, max-age=0, must-revalidate"
)

// ServeHTTP routes the site's paths; anything else is its 404 page.
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	// The files crawlers read live at the root only.
	switch r.URL.Path {
	case "/favicon.ico":
		s.headers(w, "public, max-age=86400")
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(favicon)) //nolint:errcheck // the client went away
		return
	case "/robots.txt":
		s.text(w, r, "text/plain; charset=utf-8", s.robots())
		return
	case "/sitemap.xml":
		s.text(w, r, "application/xml; charset=utf-8", s.sitemap())
		return
	case "/llms.txt":
		s.text(w, r, "text/markdown; charset=utf-8", s.llms(r.Context(), false))
		return
	case "/llms-full.txt":
		s.text(w, r, "text/markdown; charset=utf-8", s.llms(r.Context(), true))
		return
	}
	l, path := languageOf(r.URL.Path)
	switch path {
	case "/":
		s.home(w, r, l)
	case "/pricing":
		s.pricingPage(w, r, l)
	case "/faq":
		p := s.page(r.Context(), l, "/faq", l.t.FAQTitle, l.t.FAQDesc)
		if !p.contentOK {
			s.unavailable(w, r, errNoContent)
			return
		}
		p.jsonLD = []any{s.faqLD(p.faqs), s.breadcrumbLD(l, crumb{l.t.FAQ, path})}
		s.write(w, r, http.StatusOK, cachePage, p, faqPage(p, p.faqs))
	case "/features":
		p := s.page(r.Context(), l, "/features", l.t.FeaturesTitle, l.t.FeaturesDesc)
		p.jsonLD = []any{s.breadcrumbLD(l, crumb{l.t.Features, path})}
		s.write(w, r, http.StatusOK, cachePage, p, featuresIndexPage(p))
	case "/about":
		p := s.page(r.Context(), l, "/about", l.t.AboutTitle, l.t.AboutDesc)
		p.jsonLD = []any{s.organizationLD(p), s.aboutLD(p), s.breadcrumbLD(l, crumb{l.t.About, path})}
		s.write(w, r, http.StatusOK, cachePage, p, aboutPage(p))
	case "/privacy", "/terms":
		lp := l.c.privacy
		if path == "/terms" {
			lp = l.c.terms
		}
		p := s.page(r.Context(), l, lp.path, lp.title, lp.description)
		p.jsonLD = []any{s.breadcrumbLD(l, crumb{lp.name, path})}
		s.write(w, r, http.StatusOK, cachePage, p, legalPageView(p, lp))
	default:
		if slug, ok := strings.CutPrefix(path, "/features/"); ok {
			if f, ok := l.c.featureBySlug(slug); ok {
				s.feature(w, r, l, f)
				return
			}
		}
		p := s.page(r.Context(), l, path, l.t.NotFoundTitle, "")
		p.noindex = true
		s.write(w, r, http.StatusNotFound, cachePage, p, notFoundPage(p))
	}
}

// languageOf is a request path's language, and the page's path in it:
// "/en/pricing" is English's "/pricing"; anything else is Arabic's.
func languageOf(path string) (*language, string) {
	if path == english.base {
		return english, "/"
	}
	if rest, ok := strings.CutPrefix(path, english.base+"/"); ok {
		return english, "/" + rest
	}
	return arabic, path
}

func (s *Site) home(w http.ResponseWriter, r *http.Request, l *language) {
	p := s.page(r.Context(), l, "/", l.t.HomeTitle, l.t.HomeDesc)
	if !p.contentOK {
		s.unavailable(w, r, errNoContent)
		return
	}
	// The home page's prices are the main market's: the page stays the
	// same for everyone, so the edge keeps it.
	pr, err := s.pricing(r.Context(), currencyEGP, l)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	var home []faq
	for _, f := range p.faqs {
		if f.home {
			home = append(home, f)
		}
	}
	p.jsonLD = []any{s.organizationLD(p), s.websiteLD(l), s.applicationLD(l, pr)}
	if len(home) > 0 {
		p.jsonLD = append(p.jsonLD, s.faqLD(home))
	}
	s.write(w, r, http.StatusOK, cachePage, p, homePage(p, pr, home))
}

func (s *Site) pricingPage(w http.ResponseWriter, r *http.Request, l *language) {
	currency := currencyFor(r.Header.Get(s.d.CountryHeader))
	switch strings.ToUpper(r.URL.Query().Get("currency")) {
	case currencyEGP:
		currency = currencyEGP
	case currencyUSD:
		currency = currencyUSD
	}
	pr, err := s.pricing(r.Context(), currency, l)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	p := s.page(r.Context(), l, "/pricing", l.t.PricingTitle, l.t.PricingDesc)
	p.jsonLD = []any{s.applicationLD(l, pr), s.breadcrumbLD(l, crumb{l.t.Pricing, "/pricing"})}
	w.Header().Set("Vary", s.d.CountryHeader)
	s.write(w, r, http.StatusOK, cachePrivate, p, pricingPage(p, pr))
}

func (s *Site) feature(w http.ResponseWriter, r *http.Request, l *language, f featurePage) {
	p := s.page(r.Context(), l, "/features/"+f.slug, f.title, f.description)
	p.jsonLD = []any{s.breadcrumbLD(l, crumb{l.t.Features, "/features"}, crumb{f.name, p.path})}
	if len(f.faqs) > 0 {
		p.jsonLD = append(p.jsonLD, s.faqLD(f.faqs))
	}
	s.write(w, r, http.StatusOK, cachePage, p, featurePageView(p, f))
}

func (s *Site) pricing(ctx context.Context, currency string, l *language) (pricing, error) {
	o, err := s.plans.get(ctx, currency)
	if err != nil {
		return pricing{}, err
	}
	return buildPricing(o, l), nil
}

// pageCtx is what every page's frame needs. path is the page's path in
// its language (without /en).
type pageCtx struct {
	l                      *language
	t                      *texts
	c                      *siteCopy
	origin, path           string
	title, description     string
	css                    string
	fontPreload            []string
	noindex                bool
	jsonLD                 []any
	signup, login          string
	whatsApp, whatsDisplay string
	// What the console sets: contentOK is false when it could not be
	// read (the frame then shows the default number, and no FAQ).
	contentOK         bool
	settings          domain.Settings
	intl, intlDisplay string // the international WhatsApp's link
	socials           []socialLink
	faqs              []faq // in the page's language
}

// socialLink is a network the site links to, by its own name.
type socialLink struct{ name, url string }

var errNoContent = errors.New("site: the console's content cannot be read")

func (s *Site) page(ctx context.Context, l *language, path, title, description string) *pageCtx {
	// English pages set in the system's fonts: the brand face is Arabic's.
	preload := s.d.FontPreload
	if l == english {
		preload = nil
	}
	ct, err := s.d.Content.Content(ctx)
	ok := err == nil
	if !ok {
		s.d.Log.ErrorContext(ctx, "site: content unavailable", "err", err)
		ct = domain.Content{Settings: domain.Settings{WhatsApp: s.d.WhatsApp}}
	}
	set := ct.Settings
	p := &pageCtx{l: l, t: l.t, c: l.c, origin: s.origin(), path: path, title: title, description: description, css: s.css, fontPreload: preload,
		signup: strings.TrimRight(s.d.DashboardURL, "/") + "/signup", login: strings.TrimRight(s.d.DashboardURL, "/") + "/login",
		whatsApp: whatsAppLink(l, set.WhatsApp), whatsDisplay: displayPhone(set.WhatsApp),
		intl: whatsAppLink(l, set.WhatsAppIntl), intlDisplay: displayPhone(set.WhatsAppIntl),
		contentOK: ok, settings: set, faqs: faqsIn(l, ct.FAQs)}
	for _, k := range domain.Socials() {
		if u := set.Socials[k]; u != "" {
			p.socials = append(p.socials, socialLink{socialNames[k], u})
		}
	}
	return p
}

// socialNames are the networks as they write themselves.
var socialNames = map[domain.Social]string{ //nolint:gochecknoglobals // constant table
	domain.Facebook: "Facebook", domain.Instagram: "Instagram", domain.TikTok: "TikTok",
	domain.LinkedIn: "LinkedIn", domain.YouTube: "YouTube", domain.X: "X",
}

// faqsIn are the questions in l: English ones only where the console
// wrote them.
func faqsIn(l *language, list []domain.FAQ) []faq {
	var out []faq
	for _, f := range list {
		switch {
		case l == english && f.QuestionEn != "":
			out = append(out, faq{q: f.QuestionEn, a: f.AnswerEn, home: f.OnHome})
		case l != english:
			out = append(out, faq{q: f.QuestionAr, a: f.AnswerAr, home: f.OnHome})
		}
	}
	return out
}

// origin is the site's canonical origin: its own domain, never the Host
// a request came with.
func (s *Site) origin() string {
	if s.d.HTTPS {
		return "https://" + s.d.Host
	}
	return "http://" + s.d.Host
}

// whatsAppLink opens a chat with number (E.164), with a ready first
// message in l; "" without a number.
func whatsAppLink(l *language, number string) string {
	n := strings.TrimPrefix(number, "+")
	if n == "" {
		return ""
	}
	return "https://wa.me/" + n + "?text=" + url.QueryEscape(l.t.WhatsAppHello)
}

// url is the page's full address in language l.
func (p *pageCtx) url(l *language) string { return p.origin + l.at(p.path) }

// other is the language the page is not in.
func (p *pageCtx) other() *language {
	if p.l == english {
		return arabic
	}
	return english
}

func (s *Site) headers(w http.ResponseWriter, cacheControl string) {
	h := w.Header()
	h.Set("Cache-Control", cacheControl)
	h.Set("Content-Security-Policy", s.csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}

func (s *Site) write(w http.ResponseWriter, r *http.Request, status int, cacheControl string, p *pageCtx, body templ.Component) {
	var buf bytes.Buffer
	if err := layout(p, body).Render(r.Context(), &buf); err != nil {
		s.unavailable(w, r, err)
		return
	}
	s.headers(w, cacheControl)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	out := compressed(w, r, buf.Bytes())
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(out) //nolint:errcheck // the client went away
	}
}

func (s *Site) text(w http.ResponseWriter, r *http.Request, contentType, body string) {
	s.headers(w, cachePage)
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(compressed(w, r, []byte(body))) //nolint:errcheck // the client went away
}

// compressed is body gzipped when the client takes it (a page is about
// a fifth of its size: what a phone's first paint waits for), or body.
func compressed(w http.ResponseWriter, r *http.Request, body []byte) []byte {
	w.Header().Add("Vary", "Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return body
	}
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return body
	}
	if _, err = zw.Write(body); err != nil || zw.Close() != nil {
		return body
	}
	w.Header().Set("Content-Encoding", "gzip")
	return b.Bytes()
}

func (s *Site) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	s.d.Log.ErrorContext(r.Context(), "site: page unavailable", "path", r.URL.Path, "err", err)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "5")
	http.Error(w, "تعذر تحميل الصفحة الآن. حاول مرة أخرى بعد قليل.", http.StatusServiceUnavailable)
}
