package domain_test

import (
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestRoles is ADR-0077's table: what each role may do.
func TestRoles(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		role domain.Role
		want []domain.Permission
	}{
		{domain.RoleOwner, domain.AllPermissions()},
		{domain.RoleOperations, []domain.Permission{domain.PermStoresRead, domain.PermStoresManage, domain.PermContactsRead, domain.PermBillingRead, domain.PermCatalogRead}},
		{domain.RoleFinance, []domain.Permission{domain.PermStoresRead, domain.PermBillingRead, domain.PermBillingManage, domain.PermCatalogRead, domain.PermCatalogManage}},
		{domain.RoleContent, []domain.Permission{domain.PermContentRead, domain.PermContentManage}},
		{domain.RoleViewer, []domain.Permission{domain.PermStoresRead, domain.PermBillingRead, domain.PermCatalogRead, domain.PermContentRead}},
		{domain.Role("root"), nil},
	} {
		if got := tt.role.Permissions(); !slices.Equal(got, tt.want) {
			t.Errorf("%s = %v; want %v", tt.role, got, tt.want)
		}
	}
	// Only an owner manages the team or reads the log; nobody but owners
	// and operations sees a store owner's contact.
	for _, r := range domain.Roles() {
		if r.Can(domain.PermTeamManage) != (r == domain.RoleOwner) || r.Can(domain.PermAuditRead) != (r == domain.RoleOwner) {
			t.Errorf("%s: team or log", r)
		}
		if r.Can(domain.PermContactsRead) != (r == domain.RoleOwner || r == domain.RoleOperations) {
			t.Errorf("%s: contacts", r)
		}
		if got, err := domain.ParseRole(string(r)); err != nil || got != r {
			t.Errorf("parse %s = %s, %v", r, got, err)
		}
	}
	if _, err := domain.ParseRole("admin"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("unknown role: %v", err)
	}
}

// TestLeavesAnOwner: the last active owner cannot stop being one.
func TestLeavesAnOwner(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	a, b, c := ids.New(), ids.New(), ids.New()
	for _, tt := range []struct {
		owners []ulid.ID
		id     ulid.ID
		want   bool
	}{
		{[]ulid.ID{a}, a, false},
		{[]ulid.ID{a, b}, a, true},
		{[]ulid.ID{a}, c, true},
		{nil, c, true},
	} {
		if got := domain.LeavesAnOwner(tt.owners, tt.id); got != tt.want {
			t.Errorf("owners %v without %v = %v", tt.owners, tt.id, got)
		}
	}
}
