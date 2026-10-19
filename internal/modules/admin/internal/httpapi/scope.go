package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

// ClientIPFunc returns the client address resolved by RealIP.
type ClientIPFunc func(context.Context) (netip.Addr, bool)

// Scope guards every console route:
//
//   - Host must be the admin host, otherwise 404 exactly like an unknown
//     route, so the console is invisible on store and API hosts.
//   - Requests that change state must be same-origin (the session is a
//     cookie; SameSite=Strict is the first line, this is the second).
//   - Every route but login needs a live admin session (401).
func Scope(svc *app.Service, resp *problem.Responder, adminHost string, clientIP ClientIPFunc) func(http.Handler) http.Handler {
	notFound := errs.NotFound("route_not_found", "no route for this path")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.EqualFold(hostOnly(r.Host), adminHost) {
				resp.Write(w, r, notFound)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
				resp.Write(w, r, errs.Forbidden("cross-site request refused"))
				return
			}
			ctx := r.Context()
			meta := app.RequestMeta{UserAgent: r.UserAgent()}
			if clientIP != nil {
				meta.IP, _ = clientIP(ctx)
			}
			ctx = app.WithRequestMeta(ctx, meta)
			if r.URL.Path != "/admin/v1/auth/login" {
				c, err := r.Cookie(CookieName)
				if err != nil {
					resp.Write(w, r, errs.Unauthenticated("admin authentication required"))
					return
				}
				id, err := svc.Authenticate(ctx, c.Value)
				if err != nil {
					resp.Write(w, r, err)
					return
				}
				ctx = app.WithIdentity(ctx, id)
			}
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.TrimSuffix(h, ".")
}

func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
	default:
		return false
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}
