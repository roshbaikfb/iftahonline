package httpapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// ClientIPFunc returns the client address resolved by the server's RealIP
// middleware.
type ClientIPFunc func(context.Context) (netip.Addr, bool)

var (
	errCrossSite     = errs.Forbidden("cross-site request refused")
	errStoreNotFound = errs.NotFound("store_not_found", "store not found")
)

// Scope is the middleware for every API route (ADR-0006):
//
//   - It records the device (User-Agent, client IP) for sessions and audit.
//   - Credentials: the __Host-sid cookie (dashboard) or a bearer token
//     (mobile app).
//   - CSRF: a cookie-authenticated request that changes state must come
//     from our own origin (Sec-Fetch-Site: same-origin, or a matching
//     Origin). Bearer tokens are not sent automatically, so they need no
//     such check.
//   - Routes with {store_id}: no session is 401; a non-member, or a
//     malformed store ID, is 404; otherwise the tenant and principal are
//     set from one database query.
//   - Other routes: a valid session sets the identity; a missing or stale
//     one leaves the request anonymous, and handlers that need a user
//     return 401 themselves (so sign-in works with an old cookie).
func Scope(svc *app.Service, resp *problem.Responder, clientIP ClientIPFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			meta := app.RequestMeta{UserAgent: r.UserAgent()}
			if clientIP != nil {
				meta.IP, _ = clientIP(ctx)
			}
			ctx = app.WithRequestMeta(ctx, meta)

			token, fromCookie := credentials(r)
			if fromCookie && !safeMethod(r.Method) && !sameOrigin(r) {
				resp.Write(w, r, errCrossSite)
				return
			}

			if raw := r.PathValue("store_id"); raw != "" || strings.Contains(r.Pattern, "{store_id}") {
				store, err := tenant.ParseStoreID(raw)
				if err != nil {
					resp.Write(w, r, errStoreNotFound)
					return
				}
				if token == "" {
					resp.Write(w, r, errs.Unauthenticated("authentication required"))
					return
				}
				ctx = tenant.WithStore(ctx, store)
				p, err := svc.AuthenticateInStore(ctx, token)
				if err != nil {
					resp.Write(w, r, err)
					return
				}
				ctx = authz.WithPrincipal(ctx, p)
				ctx = app.WithIdentity(ctx, app.Identity{UserID: p.UserID, SessionID: p.SessionID})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if token != "" {
				id, err := svc.Authenticate(ctx, token)
				switch {
				case err == nil:
					ctx = app.WithIdentity(ctx, id)
				case !errs.Is(err, errs.KindUnauthenticated):
					resp.Write(w, r, err)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// credentials prefers an explicit bearer token over the cookie.
func credentials(r *http.Request) (token string, fromCookie bool) {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(t), false
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
	default:
		return false // same-site, cross-site, none
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}
