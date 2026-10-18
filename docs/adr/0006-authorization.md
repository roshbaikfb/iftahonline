# ADR-0006: Authorization — roles, one permission matrix, 404 for non-members

- Status: Accepted
- Date: 2026-10-18

## Context

A user can belong to several stores with a different role in each: owner,
manager, support or shipping. Every module's handlers must check
permissions in the same way. The identity module needs to call tenancy
(sign-up creates a store), while tenancy's handlers need an authorizer.
If the authorizer lived in identity, the two modules would import each
other, and Go forbids import cycles.

## Decision

1. **`internal/platform/authz` holds the shared vocabulary:**
   - `Role` and `Permission` (named `<resource>.<action>`);
   - `Principal` (user, session, store, role), carried in context;
   - the `Authorizer` interface;
   - `Policy`, a generic evaluator built from a role → permissions map.

   Modules depend only on this package.
2. **The matrix is business policy and lives in identity**
   (`identity/internal/domain`).
   - It is the single place where roles get permissions.
   - A table-driven test pins every role × permission cell.
   - The test fails when a new permission is not yet classified.
   - `NewPolicy` rejects unknown roles or permissions at startup.
3. **Dashboard API paths are store-scoped:** `/v1/stores/{store_id}/…`.
   A membership middleware:
   - authenticates the session;
   - loads the user's membership in that store, cached;
   - sets `tenant.WithStore` and `authz.WithPrincipal`.

   A non-member gets **404, exactly as for a store that does not exist**,
   so store IDs cannot be probed. `Policy.Authorize` also returns 404 if
   the principal's store differs from the tenant in context, as a defence
   in depth.
4. **Responses:** a member without the permission gets 403. A request
   without a session gets 401.
5. **Ownership:**
   - Exactly one owner per store, enforced by a partial unique index.
   - Only the owner can manage members' roles, and ownership transfer is
     an explicit operation.
6. **Cross-tenant tests are mandatory and checked.**
   - Every operation under `/v1/stores/{store_id}` must call
     `testkit.AssertCrossTenantDenied`.
   - The module's `TestMain` fails if `testkit.Registry` is missing any
     operation from the OpenAPI spec.

## Consequences

- **Positive:**
  - One mental model and one matrix.
  - Adding a module means adding permissions here and classifying them.
  - Cross-tenant coverage is enforced, not hoped for.
- **Negative:**
  - The permission list in `authz` grows with every module. That is
    intended: it is the platform's access-control catalogue.
- **Not role-based:** super-admins (ADR-0005) are not store members and
  are authorized separately.
