// Package authz holds the authorization vocabulary every module shares
// (ADR-0006): roles, permissions, the authenticated principal carried in
// context.Context, and the Authorizer interface handlers call.
//
// The role × permission matrix itself is business policy and lives in the
// identity module, which builds a Policy from it. Modules depend only on
// this package, never on identity, so identity can in turn call other
// modules (signup creates a store) without an import cycle.
package authz

import (
	"context"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Role is a store member's role.
type Role string

// Store roles. Owner is unique per store.
const (
	RoleOwner    Role = "owner"
	RoleManager  Role = "manager"
	RoleSupport  Role = "support"
	RoleShipping Role = "shipping"
)

// Roles lists every role, most privileged first.
func Roles() []Role { return []Role{RoleOwner, RoleManager, RoleSupport, RoleShipping} }

// ParseRole validates a role name from external input.
func ParseRole(s string) (Role, error) {
	for _, r := range Roles() {
		if string(r) == s {
			return r, nil
		}
	}
	return "", fmt.Errorf("authz: unknown role %q", s)
}

// Permission is one action on one kind of resource, named
// "<resource>.<action>".
type Permission string

// Permissions. New modules add theirs here and grant them in identity's
// matrix; the matrix test fails until every permission is classified.
const (
	PermStoreRead     Permission = "store.read"
	PermStoreUpdate   Permission = "store.update"
	PermDomainsRead   Permission = "domains.read"
	PermDomainsManage Permission = "domains.manage"
	PermMembersRead   Permission = "members.read"
	PermMembersManage Permission = "members.manage"
	PermAuditRead     Permission = "audit.read"

	PermCatalogRead     Permission = "catalog.read"
	PermCatalogManage   Permission = "catalog.manage"
	PermInventoryManage Permission = "inventory.manage"
	PermImportsManage   Permission = "imports.manage"

	PermStorefrontManage Permission = "storefront.manage"

	PermCustomersRead   Permission = "customers.read"
	PermCustomersManage Permission = "customers.manage"

	PermOrdersRead   Permission = "orders.read"
	PermOrdersManage Permission = "orders.manage" // create, confirm, cancel
	PermOrdersFulfil Permission = "orders.fulfil" // pack, ship, deliver, return
	PermOrdersExport Permission = "orders.export"

	PermShippingRead   Permission = "shipping.read"   // zones, rates and fees
	PermShippingManage Permission = "shipping.manage" // change them

	PermFinanceRead   Permission = "finance.read"   // cash-on-delivery reconciliation
	PermFinanceManage Permission = "finance.manage" // import carriers' statements

	PermPaymentsManage Permission = "payments.manage" // connect a card gateway
	PermPaymentsRefund Permission = "payments.refund" // refund card payments

	PermMessagingRead   Permission = "messaging.read"   // the message log and the quota
	PermMessagingManage Permission = "messaging.manage" // the store's message texts

	PermPromotionsManage Permission = "promotions.manage" // coupons

	PermMarketingManage Permission = "marketing.manage" // ad pixels and consent

	PermBillingManage Permission = "billing.manage" // the plan, the card on file, paying invoices
)

// Permissions lists every permission.
func Permissions() []Permission {
	return []Permission{
		PermStoreRead, PermStoreUpdate,
		PermDomainsRead, PermDomainsManage,
		PermMembersRead, PermMembersManage,
		PermAuditRead,
		PermCatalogRead, PermCatalogManage, PermInventoryManage, PermImportsManage,
		PermStorefrontManage,
		PermCustomersRead, PermCustomersManage,
		PermOrdersRead, PermOrdersManage, PermOrdersFulfil, PermOrdersExport,
		PermShippingRead, PermShippingManage,
		PermFinanceRead, PermFinanceManage,
		PermPaymentsManage, PermPaymentsRefund,
		PermMessagingRead, PermMessagingManage,
		PermPromotionsManage,
		PermMarketingManage,
		PermBillingManage,
	}
}

// Principal is the authenticated store member acting on a request or job.
type Principal struct {
	UserID    ulid.ID
	SessionID ulid.ID
	StoreID   tenant.StoreID
	Role      Role
}

type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p. Only the membership
// middleware calls it, after it has set the same store with
// tenant.WithStore.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal in ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && !p.UserID.IsZero()
}

// Authorizer decides whether the principal in ctx may perform perm in the
// store in ctx. Every handler calls it before doing anything else.
type Authorizer interface {
	Authorize(ctx context.Context, perm Permission) error
}

// Policy is an immutable role × permission matrix. It implements
// Authorizer.
type Policy struct {
	grants map[Role]map[Permission]struct{}
}

// NewPolicy builds a policy. Unknown roles or permissions are errors, so
// a typo in the matrix fails at startup instead of silently denying.
func NewPolicy(grants map[Role][]Permission) (Policy, error) {
	known := make(map[Permission]struct{}, len(Permissions()))
	for _, p := range Permissions() {
		known[p] = struct{}{}
	}
	pol := Policy{grants: make(map[Role]map[Permission]struct{}, len(grants))}
	for role, perms := range grants {
		if _, err := ParseRole(string(role)); err != nil {
			return Policy{}, err
		}
		set := make(map[Permission]struct{}, len(perms))
		for _, p := range perms {
			if _, ok := known[p]; !ok {
				return Policy{}, fmt.Errorf("authz: unknown permission %q", p)
			}
			set[p] = struct{}{}
		}
		pol.grants[role] = set
	}
	return pol, nil
}

// Allows reports whether role has perm.
func (p Policy) Allows(role Role, perm Permission) bool {
	_, ok := p.grants[role][perm]
	return ok
}

// Authorize implements Authorizer.
//
//   - No principal: 401.
//   - Principal for a different store than the tenant in ctx: 404, the
//     same answer a non-member gets, so store IDs cannot be probed.
//   - Role lacks perm: 403.
func (p Policy) Authorize(ctx context.Context, perm Permission) error {
	pr, ok := PrincipalFrom(ctx)
	if !ok {
		return errs.Unauthenticated("authentication required")
	}
	store, err := tenant.FromContext(ctx)
	if err != nil || store != pr.StoreID {
		return errs.NotFound("store_not_found", "store not found")
	}
	if !p.Allows(pr.Role, perm) {
		return errs.Forbidden("your role does not allow this action")
	}
	return nil
}
