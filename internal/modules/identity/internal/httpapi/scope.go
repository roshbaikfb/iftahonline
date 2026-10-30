package httpapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
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
			if opensSession(r) {
				var newID bool
				meta.DeviceID, newID = deviceID(r)
				if newID {
					w = &deviceCookieWriter{ResponseWriter: w, cookie: deviceCookie(meta.DeviceID)}
				}
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
				id := app.Identity{UserID: p.UserID, SessionID: p.SessionID}
				if !p.ImpersonatedBy.IsZero() {
					imp := app.Impersonation{Admin: p.ImpersonatedBy, Store: store, ReadOnly: p.ReadOnly}
					if !impersonationAllows(svc, w, r, resp, imp, !p.ReadOnly || safeMethod(r.Method)) {
						return
					}
					id.Impersonation = &imp
				}
				ctx = app.WithIdentity(ctx, id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if token != "" {
				id, err := svc.Authenticate(ctx, token)
				switch {
				case err == nil:
					// Outside its store an impersonation session may read
					// the account and sign out, never change it.
					if imp := id.Impersonation; imp != nil &&
						!impersonationAllows(svc, w, r, resp, *imp, safeMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/v1/auth/")) {
						return
					}
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

// impersonationAllows audits a request of an impersonation session
// (ADR-0055) and refuses it (403) unless allowed. A request that cannot be
// audited is not served.
func impersonationAllows(svc *app.Service, w http.ResponseWriter, r *http.Request, resp *problem.Responder, imp app.Impersonation, allowed bool) bool {
	route := r.Pattern
	if route == "" {
		route = r.URL.Path
	}
	if err := svc.RecordImpersonatedRequest(r.Context(), imp, r.Method, route, !allowed); err != nil {
		resp.Write(w, r, err)
		return false
	}
	if !allowed {
		resp.Write(w, r, app.ErrImpersonationReadOnly())
	}
	return allowed
}

// DeviceCookieName identifies a browser across sign-ins (new-device
// alerts). It is not a credential.
const DeviceCookieName = "__Host-did"

// opensSession reports routes that can create a session.
func opensSession(r *http.Request) bool {
	return r.Method == http.MethodPost &&
		(strings.HasPrefix(r.URL.Path, "/v1/auth/") || r.URL.Path == "/v1/invitations/accept")
}

// deviceID returns the app's X-Device-ID, the browser's device cookie, or
// a new ID (newID true: set the cookie on the response).
func deviceID(r *http.Request) (id string, newID bool) {
	if h := r.Header.Get("X-Device-ID"); domain.ValidDeviceID(h) {
		return h, false
	}
	if c, err := r.Cookie(DeviceCookieName); err == nil && domain.ValidDeviceID(c.Value) {
		return c.Value, false
	}
	id, err := domain.NewDeviceID()
	if err != nil {
		return "", false
	}
	return id, true
}

func deviceCookie(id string) string {
	return (&http.Cookie{Name: DeviceCookieName, Value: id, Path: "/", MaxAge: 2 * 365 * 24 * 3600,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}).String()
}

// deviceCookieWriter adds the device cookie when the response starts, so
// it survives handlers that Set (not Add) their own Set-Cookie.
type deviceCookieWriter struct {
	http.ResponseWriter
	cookie string
	done   bool
}

func (d *deviceCookieWriter) WriteHeader(code int) {
	if !d.done {
		d.done = true
		d.Header().Add("Set-Cookie", d.cookie)
	}
	d.ResponseWriter.WriteHeader(code)
}

func (d *deviceCookieWriter) Write(b []byte) (int, error) {
	if !d.done {
		d.WriteHeader(http.StatusOK)
	}
	return d.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (d *deviceCookieWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }

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
