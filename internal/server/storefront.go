package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/server/views"
)

// MountStorefront returns the storefront's Mount function. Static files
// (assets, under assetsPrefix) are the same on every host and skip host
// resolution. Every page first resolves the Host header to a store
// (ADR-0004):
//
//   - unknown host or closed store: platform-branded 404, inviting to
//     open a store on the platform;
//   - suspended store: 503 "temporarily unavailable";
//   - store held for billing: 503 "under maintenance" (ADR-0051), with a
//     way into the dashboard for its owner and nothing about the invoice;
//   - resolver failure (database down): 503 "try again";
//   - otherwise pages runs with the store in tenant context.
func MountStorefront(resolver tenancy.HostResolver, pages http.Handler, assetsPrefix string, assets http.Handler, u Unavailable) func(Routes) {
	return func(rt Routes) {
		rt.Mux.Handle("GET "+assetsPrefix, assets)
		rt.Mux.Handle("GET /", StoreHost(rt, resolver, u, pages))
		rt.Mux.Handle("POST /", StoreHost(rt, resolver, u, pages)) // the cart's forms
	}
}

// Unavailable is what the pages of a store that cannot be shown link to
// and look like: the platform's site, the dashboard's sign-in, and the
// brand's @font-face rules.
type Unavailable struct {
	SiteDomain   string // the platform's own site (ADR-0071)
	DashboardURL string
	FontFaces    string
}

// unavailablePages are the four pages, their stylesheet and its CSP.
type unavailablePages struct {
	u   Unavailable
	css string
	csp string
}

func newUnavailablePages(u Unavailable) unavailablePages {
	css := u.FontFaces + views.ErrorCSS
	sum := sha256.Sum256([]byte(css))
	return unavailablePages{u: u, css: css, csp: csp.Storefront("'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'")}
}

// The pages' icons, stroke paths on a 24px grid.
const (
	iconPause  = "M10 9v6M14 9v6M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18"
	iconWrench = "M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.6 2.6-2.4-.6-.6-2.4z"
	iconSearch = "M11 18a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-4.3-4.3M8.5 11h5"
	iconRetry  = "M20 12a8 8 0 1 1-2.3-5.6M20 4v5h-5"
)

const onPlatform = "متجر على منصة افتح أونلاين"

func (p unavailablePages) suspended() views.ErrorView {
	return views.ErrorView{Status: http.StatusServiceUnavailable, Tone: "amber", Icon: iconPause, TitleAR: "هذا المتجر متوقف مؤقتًا",
		BodyAR: "لا يمكن تصفّحه أو الطلب منه الآن. إن كان لديك طلب منه، تواصل مع صاحب المتجر.",
		EN:     "This store is temporarily unavailable.", Footer: onPlatform, CSS: p.css}
}

// held tells shoppers nothing about the invoice; its owner gets a way in
// to pay.
func (p unavailablePages) held() views.ErrorView {
	return views.ErrorView{Status: http.StatusServiceUnavailable, Tone: "sky", Icon: iconWrench, TitleAR: "المتجر تحت الصيانة",
		BodyAR: "نجهّز المتجر الآن، وسيعود قريبًا. شكرًا لصبرك.", EN: "This store is under maintenance and will be back soon.",
		Link:   &views.ErrorLink{Href: strings.TrimRight(p.u.DashboardURL, "/") + "/login", Label: "صاحب المتجر؟ ادخل لوحة التحكم"},
		Footer: onPlatform, CSS: p.css}
}

func (p unavailablePages) notFound(r *http.Request) views.ErrorView {
	return views.ErrorView{Status: http.StatusNotFound, Tone: "slate", Icon: iconSearch, TitleAR: "لا يوجد متجر على هذا العنوان",
		BodyAR: "تأكّد من كتابة العنوان صحيحًا، أو اسأل صاحب المتجر عن رابطه.", EN: "There is no store at this address.",
		Link:   &views.ErrorLink{Href: siteURL(p.u.SiteDomain, r) + "/", Label: "أنشئ متجرك على افتح أونلاين", Button: true},
		Footer: "افتح أونلاين: منصة متاجر مصر والخليج", CSS: p.css}
}

func (p unavailablePages) failed(r *http.Request) views.ErrorView {
	return views.ErrorView{Status: http.StatusServiceUnavailable, Tone: "red", Icon: iconRetry, TitleAR: "تعذّر تحميل الصفحة",
		BodyAR: "حدث خطأ مؤقت من جهتنا. حاول مرة أخرى بعد لحظات.", EN: "We could not load this page. Please try again shortly.",
		Link: &views.ErrorLink{Href: r.URL.EscapedPath() + query(r), Label: "أعد المحاولة"}, Footer: onPlatform, CSS: p.css}
}

// MountPlatformSite returns the Mount function of the platform's own
// site (ADR-0071) on its domain: its pages and the shop's static files
// (the brand typeface) on domain, and www.domain sent there for good.
// Host patterns are more specific than the storefront's, so domain never
// reaches store-host resolution; every other host does.
func MountPlatformSite(domain string, site http.Handler, assetsPrefix string, assets http.Handler) func(Routes) {
	return func(rt Routes) {
		rt.Mux.Handle("GET "+domain+"/", site)
		rt.Mux.Handle("GET "+domain+assetsPrefix, assets)
		rt.Mux.Handle("GET www."+domain+"/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, siteURL(domain, r)+r.URL.EscapedPath()+query(r), http.StatusMovedPermanently) //nolint:gosec // the configured domain, not the request's
		}))
	}
}

// siteURL is the platform site's origin on domain: always the configured
// domain; only a numeric port (in development) and the scheme come from
// the request.
func siteURL(domain string, r *http.Request) string {
	host := domain
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" && strings.Trim(port, "0123456789") == "" {
		host += ":" + port
	}
	scheme := "https://"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http://"
	}
	return scheme + host
}

// query is a request's query string with its "?", or "".
func query(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

// StoreHost resolves r.Host and sets the tenant for next; a store that
// cannot be shown gets one of u's pages.
func StoreHost(rt Routes, resolver tenancy.HostResolver, u Unavailable, next http.Handler) http.Handler {
	pages := newUnavailablePages(u)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := resolver.Resolve(r.Context(), r.Host)
		switch {
		case errors.Is(err, tenancy.ErrUnknownHost):
			writePage(rt, w, r, pages.csp, pages.notFound(r))
			return
		case err != nil:
			rt.Log.ErrorContext(r.Context(), "storefront: resolve host", "err", err)
			w.Header().Set("Retry-After", "5")
			writePage(rt, w, r, pages.csp, pages.failed(r))
			return
		}
		switch res.Status {
		case tenancy.StatusActive:
			next.ServeHTTP(w, r.WithContext(tenant.WithStore(r.Context(), res.StoreID)))
		case tenancy.StatusHeld:
			// The store's data is kept; it comes back when its invoice is
			// paid. Nothing tells shoppers why.
			w.Header().Set("Retry-After", "3600")
			writePage(rt, w, r, pages.csp, pages.held())
		case tenancy.StatusSuspended:
			writePage(rt, w, r, pages.csp, pages.suspended())
		default: // closed: indistinguishable from a store that never existed
			writePage(rt, w, r, pages.csp, pages.notFound(r))
		}
	})
}

// writePage sends v, never cached, under policy (its stylesheet's hash).
func writePage(rt Routes, w http.ResponseWriter, r *http.Request, policy string, v views.ErrorView) {
	var buf bytes.Buffer
	if err := views.ErrorPage(v).Render(r.Context(), &buf); err != nil {
		rt.Problems.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", policy)
	w.WriteHeader(v.Status)
	if _, err := buf.WriteTo(w); err != nil {
		rt.Log.DebugContext(r.Context(), "storefront: client went away", "err", err)
	}
}
