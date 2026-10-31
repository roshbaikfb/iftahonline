# ADR-0061: Usage meters

- Status: Accepted
- Date: 2026-10-31

## Context

The approved billing page (7f) shows five meters against what the store
is granted:
- orders this month;
- messages this month;
- products;
- team seats;
- custom domains.

The approved decision (2026-10-31) was to show all five, with each
module giving its count without exposing its tables.

The monthly counts already live in entitlements (`usage_counters`). The
other three are held by other modules:
- catalog holds the products;
- identity holds members and invitations;
- tenancy holds the domains.

Each of those modules already passes its count to `AdmitTx` when it
enforces the limit.

## Decision

1. **Each module exports its count in `api.go`,** as a function of the
   store in ctx and a tenant transaction, the same count its limit
   admits against:
   - `catalog.CountProducts`;
   - `identity.CountStaff`, which counts members (the owner included)
     and open invitations;
   - `tenancy.CountCustomDomains`.
2. **entitlements reads them all.** `entitlements.Deps.Counters` maps a
   counted feature to its `Counter`, wired in `cmd/platform`.
   `Service.Meters` reads, in one tenant transaction:
   - the grants (cached);
   - the monthly counts;
   - each counter.

   A counted feature without a counter is left out.
3. **`GET /v1/stores/{id}/usage`** (a new `entitlements` tag, permission
   `billing.manage`) returns the meters in the page's order: feature,
   kind, used, and limit (absent when unlimited).

## Consequences

- **The count is in the right place.** No module reads another's
  tables; the count is the owning module's query, run in entitlements'
  transaction, as `AdmitTx` already does.
- **The meter matches the limit.** The meter and the limit can never
  disagree: they are the same count.
- **Cost.** Opening the billing page costs up to five small counts.
