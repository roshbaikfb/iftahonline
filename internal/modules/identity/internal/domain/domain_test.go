package domain_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestMatrix pins every role × permission cell. A new permission in authz
// makes this test fail until it is added to both the matrix and this
// table: nobody gains or loses access by accident.
func TestMatrix(t *testing.T) {
	t.Parallel()
	const (
		O = authz.RoleOwner
		M = authz.RoleManager
		S = authz.RoleSupport
		H = authz.RoleShipping
	)
	want := map[authz.Permission][]authz.Role{
		authz.PermStoreRead:     {O, M, S, H},
		authz.PermStoreUpdate:   {O, M},
		authz.PermDomainsRead:   {O, M, S},
		authz.PermDomainsManage: {O, M},
		authz.PermMembersRead:   {O, M, S},
		authz.PermMembersManage: {O},
		authz.PermAuditRead:     {O, M},

		authz.PermCatalogRead:      {O, M, S, H},
		authz.PermCatalogManage:    {O, M},
		authz.PermInventoryManage:  {O, M, H},
		authz.PermImportsManage:    {O, M},
		authz.PermStorefrontManage: {O, M},

		authz.PermCustomersRead:   {O, M, S},
		authz.PermCustomersManage: {O, M, S},

		authz.PermOrdersRead:   {O, M, S, H},
		authz.PermOrdersManage: {O, M, S},
		authz.PermOrdersFulfil: {O, M, H},
		authz.PermOrdersExport: {O, M},

		authz.PermShippingRead:   {O, M, S, H},
		authz.PermShippingManage: {O, M},

		authz.PermFinanceRead:   {O, M},
		authz.PermFinanceManage: {O, M},
	}
	pol, err := domain.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	for _, perm := range authz.Permissions() {
		allowed, classified := want[perm]
		if !classified {
			t.Errorf("permission %q is not classified in this test (and maybe not in the matrix)", perm)
			continue
		}
		for _, role := range authz.Roles() {
			wantOK := false
			for _, r := range allowed {
				wantOK = wantOK || r == role
			}
			if got := pol.Allows(role, perm); got != wantOK {
				t.Errorf("%s × %s = %v, want %v", role, perm, got, wantOK)
			}
		}
	}
	if len(want) != len(authz.Permissions()) {
		t.Errorf("test lists %d permissions, authz has %d", len(want), len(authz.Permissions()))
	}

	// The policy is the Authorizer handlers use; spot-check it end to end.
	store := tenant.StoreID{ID: ulid.NewGenerator().New()}
	ctx := authz.WithPrincipal(tenant.WithStore(context.Background(), store),
		authz.Principal{UserID: ulid.NewGenerator().New(), StoreID: store, Role: M})
	if err := pol.Authorize(ctx, authz.PermMembersManage); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("manager managing members: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	if got, fe := domain.ValidateName("  أحمد علي "); fe != nil || got != "أحمد علي" {
		t.Fatalf("got %q, %v", got, fe)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("م", 101), "a\x00b", "a\nb"} {
		if _, fe := domain.ValidateName(bad); fe == nil || fe.Field != "name" {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pw, email, code string
	}{
		{"correct horse battery", "ahmed@example.com", ""},
		{"كلمة سر طويلة جدا", "ahmed@example.com", ""},
		{"short", "a@b.co", "too_short"},
		{"123456789", "a@b.co", "too_short"},
		{strings.Repeat("x", 129), "a@b.co", "too_long"},
		{strings.Repeat("a", 12), "a@b.co", "too_weak"},
		{"Password123", "a@b.co", "too_weak"},
		{"QWERTYUIOP", "a@b.co", "too_weak"},
		{"ahmedsalah2026", "ahmedsalah@example.com", "too_weak"},
		{"bob-is-my-secret", "bob@example.com", ""}, // local part under 4 chars is not checked
		{"\xff\xfe invalid utf8 x", "a@b.co", "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.pw, func(t *testing.T) {
			t.Parallel()
			fe := domain.ValidatePassword(tt.pw, tt.email)
			switch {
			case tt.code == "" && fe != nil:
				t.Fatalf("rejected: %+v", fe)
			case tt.code != "" && (fe == nil || fe.Code != tt.code || fe.Field != "password"):
				t.Fatalf("got %+v, want code %q", fe, tt.code)
			}
		})
	}
	// Length counts characters, not bytes.
	if fe := domain.ValidatePassword(strings.Repeat("مص", 5), "a@b.co"); fe != nil {
		t.Fatalf("10 Arabic characters must pass: %+v", fe)
	}
}

func TestExpiresAt(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := domain.ExpiresAt(created, created); !got.Equal(created.Add(domain.SessionIdleTimeout)) {
		t.Fatalf("fresh = %v", got)
	}
	late := created.Add(28 * 24 * time.Hour)
	if got := domain.ExpiresAt(created, late); !got.Equal(created.Add(domain.SessionMaxLifetime)) {
		t.Fatalf("capped = %v", got)
	}
}

func TestDeviceIDs(t *testing.T) {
	t.Parallel()
	a, err := domain.NewDeviceID()
	if err != nil || !domain.ValidDeviceID(a) {
		t.Fatalf("new id %q, %v", a, err)
	}
	b, _ := domain.NewDeviceID()
	if a == b || string(domain.DeviceHash(a)) == string(domain.DeviceHash(b)) || len(domain.DeviceHash(a)) != 32 {
		t.Fatal("ids and hashes must differ")
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), "has spaces in it ok", "üüüüüüüüüüüüüüüü", "semi;colon-0123456"} {
		if domain.ValidDeviceID(bad) {
			t.Fatalf("%q must be invalid", bad)
		}
	}
	for _, ok := range []string{"A1b2-C3d4_E5f6G7h8", "550e8400-e29b-41d4-a716-446655440000"} {
		if !domain.ValidDeviceID(ok) {
			t.Fatalf("%q must be valid", ok)
		}
	}
}
