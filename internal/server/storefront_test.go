package server_test

import (
	"context"
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

func TestStorefrontHostResolution(t *testing.T) {
	t.Parallel()
	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	tests := []struct {
		name       string
		resolver   fakeResolver
		wantStatus int
		wantBody   []string
		retryAfter bool
	}{
		{"unknown host gets branded 404", fakeResolver{err: tenancy.ErrUnknownHost}, http.StatusNotFound,
			[]string{"المتجر غير موجود", "Store not found", "iftahonline"}, false},
		{"closed store looks unknown", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusClosed}}, http.StatusNotFound,
			[]string{"Store not found"}, false},
		{"suspended store is unavailable", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusSuspended}}, http.StatusServiceUnavailable,
			[]string{"Store unavailable", "iftahonline"}, false},
		{"resolver failure is a temporary error", fakeResolver{err: errors.New("db down")}, http.StatusServiceUnavailable,
			[]string{"Temporary error"}, true},
		{"active store serves", fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusActive}}, http.StatusOK,
			[]string{"Coming soon"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, middleware.ProfileStorefront, server.MountStorefront(tt.resolver))
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
			if strings.Contains(body, store.String()) {
				t.Fatal("page leaks the store ID")
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
	h := server.StoreHost(rt, fakeResolver{res: tenancy.Resolution{StoreID: store, Status: tenancy.StatusActive}},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, gotErr = tenant.FromContext(r.Context())
			next.ServeHTTP(w, r)
		}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://shop.iftahonline.com/", nil))
	if gotErr != nil || got != store {
		t.Fatalf("tenant = %v, %v; want %v", got, gotErr, store)
	}
}
