# ADR-0004: Host → store resolution with a two-level cache

- Status: Accepted
- Date: 2026-10-18

## Context

Every storefront request starts by mapping the `Host` header to a store.
At 3,000 rps peak (about 300 rps reaching the origin after the edge cache),
one database query per request would waste pool connections on the
hottest, most repetitive lookup we have. The mapping changes rarely: a
merchant adds a custom domain or a store is suspended. When it does change,
every storefront instance must see the change within seconds.

Hosts come from the client. A bot sending random `Host` values must not
evict real stores from the cache or turn every request into a database
query.

## Decision

1. **Stores live at `<slug>.iftahonline.com`.** Custom domains are added by
   the merchant and must be verified first: a TXT record at
   `_iftah-verify.<domain>` and a CNAME to our edge. The super-admin API is
   served only on its own host, `admin.iftahonline.com`, never on a store
   host.
2. **Normalization.** A host is lower-cased, its port and trailing dot are
   removed, and it is converted to IDNA/punycode. IP literals and invalid
   labels are rejected. The normalizer is fuzz-tested.
3. **Lookup order:**
   - **L1:** an in-process, size-bounded LRU with a 60 s TTL, sized from
     config (GOMEMLIMIT rule).
   - **L1 negative cache:** a separate, small LRU with a short TTL for
     hosts that do not exist. A flood of random hosts fills only this
     cache and cannot evict real stores.
   - **L2:** Redis, shared by all instances, with a 10 min TTL.
   - **L3:** PostgreSQL, through `app.resolve_host(host)`.
     - The function is `SECURITY DEFINER` and returns only
       `(store_id, status)`.
     - The directory tables stay under RLS for every other access.
     - Because `FORCE ROW LEVEL SECURITY` applies to the owner too, the
       owner role gets a narrow read policy on these two tables (the owner
       could disable RLS anyway).
   - **Concurrent misses:** `singleflight` collapses misses for the same
     host into one lookup.
4. **Invalidation is part of the write.** A change to a domain or to a
   store's status inserts a River job in the same transaction (ADR-0003).
   The job:
   - deletes the Redis key;
   - publishes the host on the `tenancy:invalidate` channel.

   Every storefront instance subscribes to the channel and evicts the host
   from L1. The TTLs bound staleness if a message is lost.
5. **Responses:**
   - An unknown host gets the platform-branded 404 page.
   - A suspended store gets a 503 "store unavailable" page.
   - Neither page reveals whether a store ever existed.

## Consequences

- **Positive:**
  - A cache hit costs 0 database queries and 0 Redis round trips. Tests
    assert this with a query counter, and benchmarks are kept in the PR.
  - A change becomes visible on all instances within milliseconds,
    normally, and within 60 s at worst.
- **Negative:**
  - One more moving part, the pub/sub subscription. If it disconnects,
    the instance reconnects with backoff and falls back to TTL expiry
    while disconnected.
- **Not in this decision:** TLS issuance for custom domains (the edge's
  on-demand TLS "ask" endpoint) comes with the edge work.
