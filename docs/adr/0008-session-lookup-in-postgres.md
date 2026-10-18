# ADR-0008: Session lookup in PostgreSQL, no Redis session cache (yet)

- Status: Accepted (amends ADR-0005 §1, "Cache" bullet)
- Date: 2026-10-18

## Context

ADR-0005 planned to cache session lookups in Redis. While building
identity, it turned out that most dashboard requests are store-scoped
(`/v1/stores/{store_id}/…`). Those must also load the caller's membership
in that store, which lives under RLS and so needs a tenant transaction in
PostgreSQL anyway. A Redis session cache would save nothing on those
requests. It would also add invalidation work: revoking a session or
disabling a user would have to clear cache entries on time.

## Decision

1. **Store-scoped requests:** one indexed query, inside the tenant
   transaction, loads the live session, the user's status and the user's
   role in the store (`GetActiveSessionInStore`). The whole lookup is
   `BEGIN`, `set_config`, the query and `COMMIT`, whatever the store or
   user. Tests assert this.
2. **Other routes** (`/v1/me`, logout) use one query on `user_sessions`
   joined to `users`.
3. **Activity tracking:** `last_seen_at` and the sliding expiry are
   written at most once every 5 minutes per session.
4. **When to cache:** a Redis cache is added only if load measurements
   show the session lookup matters. When it is, revocation will delete the
   cache key and the TTL will stay short.

## Consequences

- **Positive:**
  - Revocation and disabling a user take effect on the very next request.
  - The membership check and the session check are one consistent read.
- **Negative:**
  - Each API request costs one small transaction. That is well within
    budget for dashboard traffic, which is far below storefront traffic and
    is not edge-cached.
