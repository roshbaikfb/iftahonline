package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
	"github.com/roshbaikfb/iftahonline/internal/server"
	"github.com/roshbaikfb/iftahonline/internal/server/middleware"
)

type fakeResolver struct {
	res tenancy.Resolution
	err error
}

func (f fakeResolver) Resolve(context.Context, string) (tenancy.Resolution, error) {
	return f.res, f.err
}

// storePage stands in for the shop: it shows which store it served.
var storePage = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	id, err := tenant.FromContext(r.Context())
	if err != nil {
		http.Error(w, "no tenant", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("<p>store page " + strings.ToLower(id.String()) + "</p>"))
})

var assetFile = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript")
	_, _ = w.Write([]byte("// asset"))
})

// pagesOf are the unavailable pages' links and the brand's faces.
var pagesOf = server.Unavailable{SiteDomain: "iftahonline.com", DashboardURL: "https://app.iftahonline.com", FontFaces: "@font-face{font-family:x}"}

func mountShop(r fakeResolver) func(server.Routes) {
	return server.MountStorefront(r, storePage, "/assets/", assetFile, pagesOf)
}

func TestStorefrontHostResolution(t *testing.T) {
	t.Parallel()
	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	tests := []struct {
		name       string
		resolver   fakeResolver
		wantStatus int
		wantBody   []string
		retryAfter bool
		notBody    []string
	}{
		{"unknown host gets branded 404 inviting to open a store", fakeResolver{err: tenancy.ErrUnknownHost}, http.StatusNotFound,
			[]string{"لا يوجد متجر على هذا العنوان", "There is no store at this address.", "أنشئ متجرك على افتح أونلاين", `href="http://iftahonline.com/"`, "افتح أونلاين"},
			false, []string{"/login"}},
		{"closed store looks unknown", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusClosed}}, http.StatusNotFound,
			[]string{"لا يوجد متجر على هذا العنوان"}, false, nil},
		{"suspended store is unavailable, and says nothing more", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusSuspended}}, http.StatusServiceUnavailable,
			[]string{"هذا المتجر متوقف مؤقتًا", "This store is temporarily unavailable.", "متجر على منصة افتح أونلاين"}, false, []string{"/login", "أنشئ متجرك"}},
		{"a store held for billing is under maintenance, with a way in for its owner", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusHeld}}, http.StatusServiceUnavailable,
			[]string{"المتجر تحت الصيانة", "under maintenance", "صاحب المتجر؟ ادخل لوحة التحكم", `href="https://app.iftahonline.com/login"`},
			true, []string{"فاتورة", "invoice", "أنشئ متجرك"}},
		{"resolver failure is a temporary error with a retry", fakeResolver{err: errors.New("db down")}, http.StatusServiceUnavailable,
			[]string{"تعذّر تحميل الصفحة", "Please try again shortly.", "أعد المحاولة", `href="/"`}, true, nil},
		{"active store serves its pages", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusActive}}, http.StatusOK,
			[]string{"store page " + strings.ToLower(store.String())}, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, middleware.ProfileStorefront, mountShop(tt.resolver))
			rec := f.do(t, http.MethodGet, "/")
			body := rec.Body.String()
			if rec.Code != tt.wantStatus || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("status = %d %q; body %s", rec.Code, rec.Header().Get("Content-Type"), body)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Fatalf("body lacks %q: %s", want, body)
				}
			}
			for _, not := range tt.notBody {
				if strings.Contains(body, not) {
					t.Fatalf("body has %q: %s", not, body)
				}
			}
			if tt.wantStatus != http.StatusOK {
				// Styled inline, allowed by its hash alone: the CSP stays strict.
				css, ok := between(body, "<style>", "</style>")
				sum := sha256.Sum256([]byte(css))
				csp := rec.Header().Get("Content-Security-Policy")
				if !ok || !strings.HasPrefix(css, pagesOf.FontFaces) || !strings.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") ||
					strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "script-src 'self'") {
					t.Fatalf("style %v %.40q; CSP %q", ok, css, csp)
				}
			}
			if tt.wantStatus != http.StatusOK && strings.Contains(strings.ToLower(body), strings.ToLower(store.String())) {
				t.Fatal("error page leaks the store ID")
			}
			if (rec.Header().Get("Retry-After") != "") != tt.retryAfter {
				t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

// TestStorefrontAssetsSkipResolution: static files are the same for every
// host, so they are served even when the host is unknown or the
// resolver is down.
func TestStorefrontAssetsSkipResolution(t *testing.T) {
	t.Parallel()
	for _, r := range []fakeResolver{{err: tenancy.ErrUnknownHost}, {err: errors.New("db down")}} {
		f := newFixture(t, middleware.ProfileStorefront, mountShop(r))
		rec := f.do(t, http.MethodGet, "/assets/app.0123abcd.js")
		if rec.Code != http.StatusOK || rec.Body.String() != "// asset" {
			t.Fatalf("asset with resolver %v: %d %q", r.err, rec.Code, rec.Body.String())
		}
	}
}

func TestStoreHostSetsTenant(t *testing.T) {
	t.Parallel()
	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	rt := server.Routes{
		Mux: http.NewServeMux(), Problems: problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil))),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var got tenant.StoreID
	var gotErr error
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := server.StoreHost(rt, fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusActive}}, pagesOf,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, gotErr = tenant.FromContext(r.Context())
			next.ServeHTTP(w, r)
		}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://shop.iftahonline.com/", nil))
	if gotErr != nil || got != store {
		t.Fatalf("tenant = %v, %v; want %v", got, gotErr, store)
	}
}

// TestPlatformSiteHost (ADR-0071): the platform's domain serves its own
// site and the shop's files, never a store; www goes there for good,
// keeping the path and a port; every other host is a store's.
func TestPlatformSiteHost(t *testing.T) {
	t.Parallel()
	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := tenant.FromContext(r.Context()); err == nil {
			http.Error(w, "a tenant on the platform's site", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("platform site " + r.URL.Path))
	})
	mount := func(rt server.Routes) {
		mountShop(fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusActive}})(rt)
		server.MountPlatformSite("iftahonline.com", site, "/assets/", assetFile)(rt)
	}
	f := newFixture(t, middleware.ProfileStorefront, mount)
	get := func(host, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		r.Host = host
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, r)
		return rec
	}
	for _, tt := range []struct{ host, path, want string }{
		{"iftahonline.com", "/", "platform site /"},
		{"iftahonline.com", "/pricing", "platform site /pricing"},
		{"iftahonline.com:18081", "/faq", "platform site /faq"},
		{"iftahonline.com", "/assets/f.woff2", "// asset"},
		{"souq.iftahonline.com", "/", "store page " + strings.ToLower(store.String())},
	} {
		if rec := get(tt.host, tt.path); rec.Code != http.StatusOK || rec.Body.String() != tt.want && !strings.Contains(rec.Body.String(), tt.want) {
			t.Errorf("%s%s: %d %q", tt.host, tt.path, rec.Code, rec.Body.String())
		}
	}
	for _, tt := range []struct{ host, to string }{
		{"www.iftahonline.com", "http://iftahonline.com/pricing?currency=USD"},
		{"www.iftahonline.com:18081", "http://iftahonline.com:18081/pricing?currency=USD"},
	} {
		rec := get(tt.host, "/pricing?currency=USD")
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != tt.to {
			t.Errorf("%s: %d %q", tt.host, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// between is the text between the first open and the next end.
func between(s, open, end string) (string, bool) {
	_, after, ok := strings.Cut(s, open)
	if !ok {
		return "", false
	}
	in, _, ok := strings.Cut(after, end)
	return in, ok
}
