# Module: tenancy

The tenancy module owns **stores** (the tenants) and their **domains**. It
also answers the first question of every storefront request: *which store
is this host?* Design: [ADR-0004](../adr/0004-host-resolution-cache.md).
Isolation rules: [ADR-0002](../adr/0002-shared-db-store-id-rls.md).

## Public API (`internal/modules/tenancy/api.go`)

| Symbol | Purpose |
|---|---|
| `Service` | Use cases on the store in `ctx`: `CreateStoreTx`, `GetStore`, `UpdateSettings`, `SetStatus`, and the domain operations; and `SlugAvailability(ctx, slug)`, which needs no store (identity's sign-up form asks it) |
| `HostResolver.Resolve(ctx, host)` | Maps a raw `Host` header to `Resolution{StoreID, Status}`, or returns `ErrUnknownHost` |
| `Store`, `NewStore`, `StoreSettings`, `Domain`, `Resolution`, `SlugAvailability` | DTOs (aliases of the domain types) |
| `Module.Mount` | Registers the HTTP API. It needs identity's membership middleware |
| `Module.RegisterWorkers` | Registers the module's jobs with the worker |
| `Module.ListenInvalidations` | Run by each storefront instance to receive cache invalidations |

`CreateStoreTx` runs inside the caller's transaction (ADR-0007). The
caller does the following:

1. Generates the store's ULID.
2. Sets it with `tenant.WithStore`.
3. Opens `postgres.InTenantTx`.
4. Calls `CreateStoreTx`.

The new store gets a verified, primary subdomain `<slug>.<PLATFORM_BASE_DOMAIN>`.

## Tables

| Table | Tenant key | Notes |
|---|---|---|
| `stores` | `id` (the tenant root) | `slug` is unique, a DNS label, and not in the reserved list. `status`: active, suspended, closed. Currency is EGP, SAR or AED |
| `store_domains` | `store_id` | A host is unique **among verified rows only**, so nobody can squat a domain with a pending claim. At most one primary per store, and the primary must be verified |

Both tables have forced RLS. Cross-tenant host lookups go only through
`app.resolve_host(host)`:

- It is a `SECURITY DEFINER` function.
- It returns `(store_id, store_status)` and nothing else.
- To make it work under `FORCE ROW LEVEL SECURITY`, `app_owner` has a
  read-only `directory_lookup` policy.

## Custom domains

1. The merchant adds a domain with `POST /v1/stores/{id}/domains` and gets
   a TXT record back: `_iftah-verify.<host>  iftah-verify=<token>`.
2. The `tenancy.domain.verify` job checks the record:
   - checks are retried with River's backoff, for up to 16 attempts
     (about 68 hours);
   - only one live job exists per domain;
   - the reason for each failure is stored: `txt_record_not_found` or
     `dns_error`.
3. When the record is found, the domain becomes `verified` and its cache
   entry is invalidated. If another store verified the same host first,
   the domain fails with `domain_taken`.
4. After the last attempt, the domain fails with `verification_timed_out`.
   `POST …/verify` restarts it.

Other rules:

- The platform subdomain cannot be removed.
- The primary domain cannot be removed.
- A store can have at most 5 custom domains.

TLS for custom domains (on-demand TLS at the edge) is **not** part of this
phase.

## Host resolution and caches

| Layer | What | Default |
|---|---|---|
| L1 | Per-process LRU for known hosts | `HOSTCACHE_SIZE`=20000 entries, `HOSTCACHE_TTL`=1m |
| L1 negative | Separate LRU for unknown hosts, so floods cannot evict real stores | `HOSTCACHE_NEGATIVE_SIZE`=4096, `HOSTCACHE_NEGATIVE_TTL`=15s |
| L2 | Redis `tenancy:host:<host>` | `HOSTCACHE_SHARED_TTL`=10m; negative entries use the negative TTL |
| L3 | `app.resolve_host` in PostgreSQL | — |

Concurrent misses for the same host are collapsed with `singleflight`. If
Redis is down, resolution falls back to PostgreSQL.

**Invalidation.** Any change that affects routing inserts
`tenancy.hosts.invalidate` twice in the same transaction: once immediately
and once 10 s later. The changes are:

- a new store (its host may sit in a negative cache);
- a status change;
- a verification;
- a domain removal.

The job deletes the Redis keys and publishes the hosts on
`tenancy:invalidate`. Every storefront instance evicts them from L1. The
second, delayed run removes any entry that a concurrent lookup re-cached
from a row read just before the commit.

**Measured** (`go test -bench . ./internal/modules/tenancy/internal/app/`,
28-core dev machine):

| Benchmark | Result |
|---|---|
| L1 hit | ~70 ns/op, 0 allocs |
| L1 hit, parallel | ~170 ns/op, 0 allocs |
| Negative hit | ~50 ns/op, 0 allocs |

`TestHostResolutionEndToEnd` asserts, on real PostgreSQL and Redis:

- a miss costs exactly 1 query;
- a hit costs 0 queries and 0 Redis commands;
- a second instance is served from Redis with 0 queries;
- a suspension reaches both instances.

## Storefront behaviour

| Resolution | Response |
|---|---|
| Active | The page, with the store in tenant context |
| Unknown host or closed store | Branded 404 (indistinguishable) |
| Suspended | 503 "store unavailable" |
| Resolver error | 503 with `Retry-After: 5` |

## HTTP API (tag `tenancy`)

| Operation | Permission |
|---|---|
| `getStore` | `store.read` |
| `updateStore` | `store.update` (name, locale, timezone) |
| `listDomains` | `domains.read` |
| `addDomain`, `deleteDomain`, `verifyDomain`, `setPrimaryDomain` | `domains.manage` |

Access checks:

- A non-member, and a malformed store or domain ID, get 404.
- Every operation has a cross-tenant test. `TestMain` fails the package if
  one is missing.
- In the `api` role the routes run behind identity's real membership
  middleware and role × permission policy.

## Audit

Every change is written to identity's audit log in the same transaction
as the change, through the `audit.Recorder` the module receives:
`store.updated`, `store.status_changed`, `domain.added`,
`domain.verification_requested`, `domain.verified`, `domain.removed` and
`domain.primary_set`.

## Events and jobs

| Kind | Scope | Args |
|---|---|---|
| `tenancy.domain.verify` | tenant | `domain_id` |
| `tenancy.hosts.invalidate` | tenant | `hosts` |
