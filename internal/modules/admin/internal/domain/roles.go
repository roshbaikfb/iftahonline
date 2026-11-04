package domain

import (
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Role is what a console admin may do (ADR-0077).
type Role string

// The roles.
const (
	RoleOwner      Role = "owner"      // everything, the team and the admin log
	RoleOperations Role = "operations" // stores, support sessions, overrides
	RoleFinance    Role = "finance"    // invoices, payments, plans, founding, agencies
	RoleContent    Role = "content"    // the site, guides and ads
	RoleViewer     Role = "viewer"     // every page, no action
)

// Roles are every role, the most powerful first.
func Roles() []Role { return []Role{RoleOwner, RoleOperations, RoleFinance, RoleContent, RoleViewer} }

// ParseRole reads a role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !slices.Contains(Roles(), r) {
		return "", errs.Validation(errs.FieldError{Field: "role", Code: "invalid", Message: "owner, operations, finance, content or viewer"})
	}
	return r, nil
}

// Permission is one thing an admin may do: an area, read or manage.
type Permission string

// The permissions.
const (
	PermStoresRead    Permission = "stores.read"
	PermStoresManage  Permission = "stores.manage" // suspend, reactivate, support sessions, overrides
	PermContactsRead  Permission = "contacts.read" // a store owner's phone and email
	PermBillingRead   Permission = "billing.read"
	PermBillingManage Permission = "billing.manage" // settle, refunds, the CSV export
	PermCatalogRead   Permission = "catalog.read"   // plans, features, founding, agencies
	PermCatalogManage Permission = "catalog.manage"
	PermContentRead   Permission = "content.read" // the site, guides, ads
	PermContentManage Permission = "content.manage"
	PermAuditRead     Permission = "audit.read"
	PermTeamManage    Permission = "team.manage"
)

// AllPermissions are every permission, in a stable order.
func AllPermissions() []Permission {
	return []Permission{PermStoresRead, PermStoresManage, PermContactsRead, PermBillingRead, PermBillingManage, PermCatalogRead,
		PermCatalogManage, PermContentRead, PermContentManage, PermAuditRead, PermTeamManage}
}

// Permissions are what r may do, in AllPermissions' order.
func (r Role) Permissions() []Permission {
	var out []Permission
	for _, p := range AllPermissions() {
		if r.Can(p) {
			out = append(out, p)
		}
	}
	return out
}

// Can reports whether r grants p.
func (r Role) Can(p Permission) bool {
	switch r {
	case RoleOwner:
		return true
	case RoleOperations:
		return slices.Contains([]Permission{PermStoresRead, PermStoresManage, PermContactsRead, PermBillingRead, PermCatalogRead}, p)
	case RoleFinance:
		return slices.Contains([]Permission{PermStoresRead, PermBillingRead, PermBillingManage, PermCatalogRead, PermCatalogManage}, p)
	case RoleContent:
		return slices.Contains([]Permission{PermContentRead, PermContentManage}, p)
	case RoleViewer:
		return slices.Contains([]Permission{PermStoresRead, PermBillingRead, PermCatalogRead, PermContentRead}, p)
	}
	return false
}

// LeavesAnOwner reports whether the console keeps an active owner once
// admin id, one of owners or not, stops being one (ADR-0078).
func LeavesAnOwner(owners []ulid.ID, id ulid.ID) bool {
	return len(owners) > 1 || !slices.Contains(owners, id)
}
