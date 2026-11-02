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
)

//go:embed assets/dist/site.css
var siteCSS string

// Deps are the site's dependencies.
type Deps struct {
	Plans Plans
	// FontFaces and FontPreload are the brand typeface the shop serves.
	FontFaces   string
	FontPreload []string
	// Host is the platform's domain the site answers on (iftahonline.com);
	// HTTPS says whether its links are https.
	Host  string
	HTTPS bool
	// DashboardURL is where merchants sign up and sign in.
	DashboardURL string
	// WhatsApp is the platform's support number in E.164 (+20…).
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
	if d.Plans == nil || d.Host == "" || d.DashboardURL == "" || d.Log == nil {
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

// Cache lifetimes: pages change when the copy or the plans do; the
// pricing page depends on the visitor's country, so the edge never
// keeps it.
const (
	cachePage    = "public, max-age=300, s-maxage=3600"
	cachePrivate = "private, max-age=0, must-revalidate"
)

// ServeHTTP routes the site's paths; anything else is its 404 page.
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/favicon.ico":
		s.headers(w, "public, max-age=86400")
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(favicon)) //nolint:errcheck // the client went away
	case "/robots.txt":
		s.text(w, r, "text/plain; charset=utf-8", s.robots())
	case "/sitemap.xml":
		s.text(w, r, "application/xml; charset=utf-8", s.sitemap())
	case "/llms.txt":
		s.text(w, r, "text/markdown; charset=utf-8", s.llms(r.Context(), false))
	case "/llms-full.txt":
		s.text(w, r, "text/markdown; charset=utf-8", s.llms(r.Context(), true))
	case "/":
		s.home(w, r)
	case "/pricing":
		s.pricingPage(w, r)
	case "/faq":
		p := s.page("/faq", "الأسئلة الشائعة عن افتح أونلاين",
			"إجابات واضحة عن افتح أونلاين: البدء مجانًا، وتأكيد الطلبات على واتساب، والحماية من الطلبات الوهمية، والأسعار بالجنيه والدولار، ونقل متجرك.")
		p.jsonLD = []any{s.faqLD(faqs), s.breadcrumbLD(crumb{"الأسئلة الشائعة", p.path})}
		s.write(w, r, http.StatusOK, cachePage, p, faqPage(p, faqs))
	case "/features":
		p := s.page("/features", "مميزات افتح أونلاين | كل ما يحتاجه متجر الدفع عند الاستلام",
			"مميزات افتح أونلاين: تأكيد الطلبات على واتساب، والحماية من الطلبات الوهمية، ولوحة الربح الصافي، والشحن والتحصيل، ومتجر احترافي، وأدوات التسويق.")
		p.jsonLD = []any{s.breadcrumbLD(crumb{"المميزات", p.path})}
		s.write(w, r, http.StatusOK, cachePage, p, featuresIndexPage(p))
	case "/about":
		p := s.page("/about", "عن افتح أونلاين | من نحن",
			"افتح أونلاين منصة متاجر إلكترونية لتجار الدفع عند الاستلام في مصر والخليج، علامة تجارية لشركة Codezilo Ltd المسجلة في المملكة المتحدة.")
		p.jsonLD = []any{s.organizationLD(), s.aboutLD(p), s.breadcrumbLD(crumb{"عن افتح أونلاين", p.path})}
		s.write(w, r, http.StatusOK, cachePage, p, aboutPage(p))
	case privacy.path, terms.path:
		lp := privacy
		if r.URL.Path == terms.path {
			lp = terms
		}
		p := s.page(lp.path, lp.title, lp.description)
		p.jsonLD = []any{s.breadcrumbLD(crumb{lp.name, p.path})}
		s.write(w, r, http.StatusOK, cachePage, p, legalPageView(p, lp))
	default:
		if f, ok := featureBySlug(strings.TrimPrefix(r.URL.Path, "/features/")); ok && strings.HasPrefix(r.URL.Path, "/features/") {
			s.feature(w, r, f)
			return
		}
		p := s.page(r.URL.Path, "الصفحة غير موجودة | افتح أونلاين", "")
		p.noindex = true
		s.write(w, r, http.StatusNotFound, cachePage, p, notFoundPage(p))
	}
}

func (s *Site) home(w http.ResponseWriter, r *http.Request) {
	p := s.page("/", "افتح أونلاين | منصة متاجر إلكترونية للدفع عند الاستلام",
		"أنشئ متجرك الإلكتروني مجانًا على افتح أونلاين: تأكيد الطلبات على واتساب، وحماية من الطلبات الوهمية، ولوحة للربح الصافي. لتجار مصر والخليج.")
	// The home page's prices are the main market's: the page stays the
	// same for everyone, so the edge keeps it.
	pr, err := s.pricing(r.Context(), currencyEGP)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	var home []faq
	for _, f := range faqs {
		if f.home {
			home = append(home, f)
		}
	}
	p.jsonLD = []any{s.organizationLD(), s.websiteLD(), s.applicationLD(pr), s.faqLD(home)}
	s.write(w, r, http.StatusOK, cachePage, p, homePage(p, pr, home))
}

func (s *Site) pricingPage(w http.ResponseWriter, r *http.Request) {
	currency := currencyFor(r.Header.Get(s.d.CountryHeader))
	switch strings.ToUpper(r.URL.Query().Get("currency")) {
	case currencyEGP:
		currency = currencyEGP
	case currencyUSD:
		currency = currencyUSD
	}
	pr, err := s.pricing(r.Context(), currency)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	p := s.page("/pricing", "أسعار افتح أونلاين | ابدأ مجانًا",
		"أسعار افتح أونلاين: باقة مجانية دائمًا، أو بلا اشتراك بنسبة من الطلبات المسلّمة، أو باقات شهرية وسنوية بتجربة 14 يومًا. بالجنيه في مصر وبالدولار في باقي الدول.")
	p.jsonLD = []any{s.applicationLD(pr), s.breadcrumbLD(crumb{"الأسعار", p.path})}
	w.Header().Set("Vary", s.d.CountryHeader)
	s.write(w, r, http.StatusOK, cachePrivate, p, pricingPage(p, pr))
}

func (s *Site) feature(w http.ResponseWriter, r *http.Request, f featurePage) {
	p := s.page("/features/"+f.slug, f.title, f.description)
	p.jsonLD = []any{s.breadcrumbLD(crumb{"المميزات", "/features"}, crumb{f.name, p.path})}
	if len(f.faqs) > 0 {
		p.jsonLD = append(p.jsonLD, s.faqLD(f.faqs))
	}
	s.write(w, r, http.StatusOK, cachePage, p, featurePageView(p, f))
}

func (s *Site) pricing(ctx context.Context, currency string) (pricing, error) {
	o, err := s.plans.get(ctx, currency)
	if err != nil {
		return pricing{}, err
	}
	return buildPricing(o), nil
}

// pageCtx is what every page's frame needs.
type pageCtx struct {
	origin, path           string
	title, description     string
	css                    string
	fontPreload            []string
	noindex                bool
	jsonLD                 []any
	signup, login          string
	whatsApp, whatsDisplay string
}

func (s *Site) page(path, title, description string) *pageCtx {
	return &pageCtx{origin: s.origin(), path: path, title: title, description: description, css: s.css, fontPreload: s.d.FontPreload,
		signup: strings.TrimRight(s.d.DashboardURL, "/") + "/signup", login: strings.TrimRight(s.d.DashboardURL, "/") + "/login",
		whatsApp: s.whatsAppLink(), whatsDisplay: displayPhone(s.d.WhatsApp)}
}

// origin is the site's canonical origin: its own domain, never the Host
// a request came with.
func (s *Site) origin() string {
	if s.d.HTTPS {
		return "https://" + s.d.Host
	}
	return "http://" + s.d.Host
}

// whatsAppLink opens a chat with the platform's support number, with a
// ready first message.
func (s *Site) whatsAppLink() string {
	n := strings.TrimPrefix(s.d.WhatsApp, "+")
	if n == "" {
		return ""
	}
	return "https://wa.me/" + n + "?text=" + url.QueryEscape("مرحبًا، أريد الاستفسار عن افتح أونلاين")
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
