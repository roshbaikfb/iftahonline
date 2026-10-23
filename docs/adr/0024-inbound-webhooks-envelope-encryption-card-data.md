# ADR-0024: Inbound webhooks, envelope-encrypted credentials, no card data

- Status: Accepted
- Date: 2026-10-23

## Context

P5 connects stores to carriers (Bosta first) and payment gateways (Paymob
first). Both talk back through webhooks, and both need the merchant's
own API credentials. The phase's acceptance asks that:

- a webhook is stored as received, verified, processed once per provider
  event, and changes state only after the provider is re-queried;
- replaying a webhook five times changes state once;
- a forged webhook is rejected and logged;
- merchant credentials are encrypted at rest with envelope encryption,
  the key coming from the environment or a KMS;
- no card-number field exists anywhere (a CI check).

Carriers and gateways are two modules (shipping, payments) with the same
webhook needs, and more providers will follow.

## Decision

1. **One `webhooks` module receives every provider's deliveries.**
   - **The URL.** Each store connection has its own URL:
     `POST /v1/webhooks/{provider}/{store_id}/{connection_id}`. It is
     public (no session) and is not a strict handler, because the raw
     body must reach the signature check byte for byte.
   - **The tenant comes from the path.** The store in the path becomes
     the tenant for the connection lookup. RLS confines that lookup to
     the store, so another store's connection is 404, and no lookup
     ever crosses tenants.
   - **The provider contract** (`webhooks.Provider`). The provider's
     module implements it and registers it at startup, the way event
     subscribers are registered:
     - `Verify` checks the delivery against the connection's secret, by
       the provider's own scheme (an HMAC, or a secret header), with
       constant-time comparison. It returns the provider's event id, or
       `ErrForged`, or `ErrUnknownConnection`.
     - `AcceptTx` enqueues the module's processing job in the
       transaction that stores the receipt.
   - **Nothing is stored before verification.**
     - **A forged delivery** is 401. It is logged with the reason, the
       client's network prefix and the body's SHA-256, never the body,
       its headers or a secret. Storing forged bodies would let anyone
       fill the database.
     - **Unknown providers, stores and connections** are 404, and logged
       the same way.
     - **A verified delivery without a usable event id** is 400.
   - **Once per event.** Accepting a delivery is one transaction:
     - a receipt, unique on `(store_id, provider, event_id)`, inserted
       with `ON CONFLICT DO NOTHING`;
     - the raw body;
     - the provider's job.

     A replay matches the unique key and answers 200 `duplicate`,
     enqueueing nothing, so the provider stops retrying. Concurrent
     replays wait on the unique index; one wins. Five sequential and
     five concurrent deliveries of one event make one receipt and one
     job (tested).
   - **The processing job re-queries the provider** (payment status,
     shipment tracking) and changes state only from that answer. It
     records an outcome on the receipt (`MarkProcessedTx`; the first
     outcome wins) in the same transaction. A signed delivery is
     therefore only ever a hint to look.
   - **Retention.**
     - **Receipts** are small and kept 90 days. Each new receipt first
       deletes a few of its store's expired ones, so an event id older
       than the retention counts as new. FORCE RLS rules out a global
       purge (ADR-0021).
     - **Bodies** are the bulk. They live in `webhook_bodies`, which is
       partitioned by month on the receipt's ULID time and kept 3 whole
       months (decision 4).
   - **Limits.**
     - At most 64 KiB per body.
     - Rate limits (Redis) of 1,200/min per client IP and 600/min per
       connection. Both are generous, because providers burst when they
       replay backlogs; a 429 just makes them retry.
     - If Redis is down, the answer is 503, which providers also retry.
2. **Envelope encryption for credentials (`platform/envelope`).**
   - **Each secret gets its own random 256-bit data key (DEK).** The
     secret is sealed with AES-256-GCM under it. The DEK is wrapped by a
     key-encryption key (KEK). One self-describing blob holds the KEK's
     id, the wrapped DEK, the nonce and the ciphertext.
   - **The KEK is an interface.** The first holder is the environment:
     the platform keyring (`SECRETBOX_KEYS`, ADR-0005), whose versioned
     keys already rotate. A KMS holder can be added later without
     touching stored blobs, since a Sealer opens blobs of every KEK it
     is given.
   - **AAD binding.** The caller's AAD (e.g.
     `payments:paymob:<store>:<connection>`) binds both the secret and
     its wrapped DEK. A blob copied to another row does not open, and a
     DEK cannot be moved between blobs.
   - **Rotation rewraps DEKs only** (`NeedsRewrap`, `Rewrap`). Rewrap
     proves the DEK opens the secret before writing.
   - **Errors.** A KEK that cannot be reached is reported as an outage,
     not as a forged blob.
   - **Fuzzing.** `FuzzOpen` checks that nothing but a real blob opens.
3. **No card data, enforced by a test.**
   - Shoppers pay only on the gateway's hosted page; no card field ever
     reaches our servers.
   - `internal/tools/nocarddata` walks every source kind (Go, SQL, the
     OpenAPI contract, templates, the dashboard, the Flutter app). It
     fails on names of card numbers, security codes, expiry dates and
     track data, and on `cc-*` autocomplete tokens.
   - It runs in `make test`, so it fails `make ci`.
   - A line that must name one (a provider's signed field list carrying
     a masked value) ends with `nocarddata:allow <reason>`. That makes
     the exception visible in review.
4. **Monthly partitions can expire.**
   - `app.monthly_partitioned` gains an optional `retain_months`, and
     `app.drop_expired_partitions()` detaches and drops the partitions
     of whole months older than that. The function is SECURITY DEFINER,
     takes no names from the caller, and only touches the registered
     tables' own `<parent>_YYYY_MM` partitions.
   - The platform partition job calls it daily. DETACH waits at most
     5 s for the parent's lock; a busy day is retried the next day.
   - Tables without a retention (orders, the audit log) are untouched.
5. **The rest of P5 builds on this** (shipping, carriers, COD
   reconciliation, payments):
   - each carrier or gateway adapter implements the module's Provider;
   - credentials are sealed with `envelope`, AAD bound to the
     connection;
   - one contract test suite per interface runs against the Fake
     adapter in `make ci`, and against the provider's sandbox when its
     keys are present.

   The orders changes that P5 needs (shipping and COD fees, payment
   state, transitions made by a carrier) land with the PR that first
   uses each one, not ahead of it.

## Consequences

- **Positive:**
  - Every provider gets verification, raw storage, once-only
    acceptance, logging and rate limits from one place. Adding a
    provider is a Verify and a job.
  - Replays, provider retries and concurrent deliveries cannot double a
    state change. Even a valid signature changes nothing until the
    provider confirms.
  - Credentials stay safe if the database leaks, and a KMS can replace
    the environment key without re-encrypting secrets.
  - Adding a card field anywhere fails CI.
- **Negative:**
  - **One URL per connection** must be registered with each provider.
    The adapters do it through the provider's API where it exists;
    otherwise the dashboard shows it to the merchant.
  - **Forged deliveries are only logged.** There is no metric yet;
    alerting reads the log line `webhook rejected`.
  - **The store ID in the webhook URL is visible to the provider.** It
    is a public ULID, not a secret, and grants nothing without the
    connection's secret.
  - **Partition drops take the parent's lock briefly** each day. Under
    heavy webhook traffic the drop may wait a day.
