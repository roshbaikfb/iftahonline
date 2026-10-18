package testkit

import (
	"net/http"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// FakeMember is a test user: their ID and their role in each store.
type FakeMember struct {
	UserID ulid.ID
	Roles  map[tenant.StoreID]authz.Role
}

// FakeStoreScope is a stand-in for identity's membership middleware with
// the same contract (ADR-0006), so a module's endpoint tests do not depend
// on identity:
//
//   - no or unknown "Authorization: Bearer <token>": 401;
//   - {store_id} malformed, or the user is not a member: 404;
//   - otherwise tenant.WithStore and authz.WithPrincipal are set.
func FakeStoreScope(resp *problem.Responder, members map[string]FakeMember) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			m, known := members[token]
			if !ok || !known {
				resp.Write(w, r, errs.Unauthenticated("authentication required"))
				return
			}
			store, err := tenant.ParseStoreID(r.PathValue("store_id"))
			role, member := m.Roles[store]
			if err != nil || !member {
				resp.Write(w, r, errs.NotFound("store_not_found", "store not found"))
				return
			}
			ctx := tenant.WithStore(r.Context(), store)
			ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: m.UserID, StoreID: store, Role: role})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
