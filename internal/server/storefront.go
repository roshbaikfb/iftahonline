package server

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/server/views"
)

// MountStorefront returns the storefront's Mount function. Static files
// (assets, under assetsPrefix) are the same on every host and skip host
// resolution. Every page first resolves the Host header to a store
// (ADR-0004):
//
//   - unknown host or closed store: platform-branded 404;
//   - suspended store: 503 "store unavailable";
//   - resolver failure (database down): 503 "try again";
//   - otherwise pages runs with the store in tenant context.
func MountStorefront(resolver tenancy.HostResolver, pages http.Handler, assetsPrefix string, assets http.Handler) func(Routes) {
	return func(rt Routes) {
		rt.Mux.Handle("GET "+assetsPrefix, assets)
		rt.Mux.Handle("GET /", StoreHost(rt, resolver, pages))
	}
}

// StoreHost resolves r.Host and sets the tenant for next.
func StoreHost(rt Routes, resolver tenancy.HostResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := resolver.Resolve(r.Context(), r.Host)
		switch {
		case errors.Is(err, tenancy.ErrUnknownHost):
			notFoundPage(rt, w, r)
			return
		case err != nil:
			rt.Log.ErrorContext(r.Context(), "storefront: resolve host", "err", err)
			w.Header().Set("Retry-After", "5")
			writePage(rt, w, r, http.StatusServiceUnavailable, "no-store", views.ErrorPage(http.StatusServiceUnavailable,
				"خطأ مؤقت", "Temporary error",
				"تعذر تحميل الصفحة الآن. حاول مرة أخرى بعد قليل.", "We could not load this page. Please try again shortly."))
			return
		}
		switch res.Status {
		case tenancy.StatusActive:
			next.ServeHTTP(w, r.WithContext(tenant.WithStore(r.Context(), res.StoreID)))
		case tenancy.StatusSuspended:
			writePage(rt, w, r, http.StatusServiceUnavailable, "no-store", views.ErrorPage(http.StatusServiceUnavailable,
				"المتجر غير متاح حاليًا", "Store unavailable",
				"هذا المتجر غير متاح في الوقت الحالي.", "This store is not available right now."))
		default: // closed: indistinguishable from a store that never existed
			notFoundPage(rt, w, r)
		}
	})
}

func notFoundPage(rt Routes, w http.ResponseWriter, r *http.Request) {
	writePage(rt, w, r, http.StatusNotFound, "no-store", views.ErrorPage(http.StatusNotFound,
		"المتجر غير موجود", "Store not found",
		"لم نعثر على متجر بهذا العنوان.", "We could not find a store at this address."))
}

func writePage(rt Routes, w http.ResponseWriter, r *http.Request, status int, cacheControl string, page templ.Component) {
	var buf bytes.Buffer
	if err := page.Render(r.Context(), &buf); err != nil {
		rt.Problems.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		rt.Log.DebugContext(r.Context(), "storefront: client went away", "err", err)
	}
}
