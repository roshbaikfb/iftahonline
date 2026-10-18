package authz_test

import (
	"context"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestParseRole(t *testing.T) {
	t.Parallel()
	for _, r := range authz.Roles() {
		got, err := authz.ParseRole(string(r))
		if err != nil || got != r {
			t.Fatalf("ParseRole(%q) = %q, %v", r, got, err)
		}
	}
	for _, bad := range []string{"", "Owner", "admin", "owner "} {
		if _, err := authz.ParseRole(bad); err == nil {
			t.Fatalf("ParseRole(%q) must fail", bad)
		}
	}
}

func TestNewPolicyRejectsUnknown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		grants map[authz.Role][]authz.Permission
	}{
		{"unknown role", map[authz.Role][]authz.Permission{"admin": {authz.PermStoreRead}}},
		{"unknown permission", map[authz.Role][]authz.Permission{authz.RoleOwner: {"store.nuke"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := authz.NewPolicy(tt.grants); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestPermissionsAreUnique(t *testing.T) {
	t.Parallel()
	seen := map[authz.Permission]bool{}
	for _, p := range authz.Permissions() {
		if seen[p] {
			t.Fatalf("duplicate permission %q", p)
		}
		seen[p] = true
	}
}

func TestPolicyAuthorize(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	storeA := tenant.StoreID{ID: gen.New()}
	storeB := tenant.StoreID{ID: gen.New()}
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:   {authz.PermStoreRead, authz.PermStoreUpdate},
		authz.RoleSupport: {authz.PermStoreRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	as := func(role authz.Role, principalStore, ctxStore tenant.StoreID) context.Context {
		ctx := tenant.WithStore(t.Context(), ctxStore)
		return authz.WithPrincipal(ctx, authz.Principal{UserID: gen.New(), StoreID: principalStore, Role: role})
	}

	tests := []struct {
		name string
		ctx  context.Context
		perm authz.Permission
		want errs.Kind
		ok   bool
	}{
		{"owner update", as(authz.RoleOwner, storeA, storeA), authz.PermStoreUpdate, 0, true},
		{"support read", as(authz.RoleSupport, storeA, storeA), authz.PermStoreRead, 0, true},
		{"support update denied", as(authz.RoleSupport, storeA, storeA), authz.PermStoreUpdate, errs.KindForbidden, false},
		{"role without grants", as(authz.RoleShipping, storeA, storeA), authz.PermStoreRead, errs.KindForbidden, false},
		{"no principal", tenant.WithStore(t.Context(), storeA), authz.PermStoreRead, errs.KindUnauthenticated, false},
		{"principal for other store", as(authz.RoleOwner, storeA, storeB), authz.PermStoreRead, errs.KindNotFound, false},
		{"principal without tenant", authz.WithPrincipal(t.Context(), authz.Principal{UserID: gen.New(), StoreID: storeA, Role: authz.RoleOwner}), authz.PermStoreRead, errs.KindNotFound, false},
		{"zero user is no principal", authz.WithPrincipal(tenant.WithStore(t.Context(), storeA), authz.Principal{StoreID: storeA, Role: authz.RoleOwner}), authz.PermStoreRead, errs.KindUnauthenticated, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := pol.Authorize(tt.ctx, tt.perm)
			if tt.ok {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errs.Is(err, tt.want) {
				t.Fatalf("err = %v, want kind %v", err, tt.want)
			}
		})
	}
}
