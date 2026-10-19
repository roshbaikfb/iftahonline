package domain

import (
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
)

// Matrix is THE role × permission matrix (ADR-0006): the only place roles
// get permissions. TestMatrix pins every cell; adding a permission to
// authz fails that test until it is classified here.
//
//   - owner: everything.
//   - manager: runs the store day to day, but cannot change who has access.
//   - support: answers customers; reads, does not configure.
//   - shipping: fulfils orders; sees the catalog and keeps stock counts
//     right (they handle the goods), but does not edit products.
func Matrix() map[authz.Role][]authz.Permission {
	return map[authz.Role][]authz.Permission{
		authz.RoleOwner: authz.Permissions(),
		authz.RoleManager: {
			authz.PermStoreRead, authz.PermStoreUpdate,
			authz.PermDomainsRead, authz.PermDomainsManage,
			authz.PermMembersRead, authz.PermAuditRead,
			authz.PermCatalogRead, authz.PermCatalogManage, authz.PermInventoryManage,
		},
		authz.RoleSupport: {
			authz.PermStoreRead, authz.PermDomainsRead, authz.PermMembersRead,
			authz.PermCatalogRead,
		},
		authz.RoleShipping: {
			authz.PermStoreRead, authz.PermCatalogRead, authz.PermInventoryManage,
		},
	}
}

// NewPolicy builds the authorizer from Matrix.
func NewPolicy() (authz.Policy, error) { return authz.NewPolicy(Matrix()) }
