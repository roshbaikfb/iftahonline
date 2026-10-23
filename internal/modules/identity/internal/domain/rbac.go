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
//   - support: answers customers; reads, does not configure, but keeps
//     the block list and confirms or cancels orders (they make the
//     confirmation calls and spot fake orders).
//   - everyone reads shipping zones and rates (support quotes them to
//     callers, shipping staff plan deliveries); only the owner and
//     manager change them.
//   - money (cash-on-delivery reconciliation, card refunds) is the
//     owner's and the manager's; only the owner connects the card gateway
//     (where the store's money is paid out).
//   - shipping: fulfils orders (packs, ships, records deliveries and
//     returns); sees the catalog and keeps stock counts right (they
//     handle the goods), but does not edit products or confirm orders.
func Matrix() map[authz.Role][]authz.Permission {
	return map[authz.Role][]authz.Permission{
		authz.RoleOwner: authz.Permissions(),
		authz.RoleManager: {
			authz.PermStoreRead, authz.PermStoreUpdate,
			authz.PermDomainsRead, authz.PermDomainsManage,
			authz.PermMembersRead, authz.PermAuditRead,
			authz.PermCatalogRead, authz.PermCatalogManage, authz.PermInventoryManage, authz.PermImportsManage,
			authz.PermStorefrontManage,
			authz.PermCustomersRead, authz.PermCustomersManage,
			authz.PermOrdersRead, authz.PermOrdersManage, authz.PermOrdersFulfil, authz.PermOrdersExport,
			authz.PermShippingRead, authz.PermShippingManage,
			authz.PermFinanceRead, authz.PermFinanceManage,
			authz.PermPaymentsRefund,
		},
		authz.RoleSupport: {
			authz.PermStoreRead, authz.PermDomainsRead, authz.PermMembersRead,
			authz.PermCatalogRead,
			authz.PermCustomersRead, authz.PermCustomersManage,
			authz.PermOrdersRead, authz.PermOrdersManage,
			authz.PermShippingRead,
		},
		authz.RoleShipping: {
			authz.PermStoreRead, authz.PermCatalogRead, authz.PermInventoryManage,
			authz.PermOrdersRead, authz.PermOrdersFulfil,
			authz.PermShippingRead,
		},
	}
}

// NewPolicy builds the authorizer from Matrix.
func NewPolicy() (authz.Policy, error) { return authz.NewPolicy(Matrix()) }
