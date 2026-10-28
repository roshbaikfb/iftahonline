# Module: webhooks

The webhooks module receives **inbound webhooks** from carriers, payment
gateways and messaging channels: it verifies each delivery through its provider, stores
the raw body, and lets each provider event through **once**, to a
processing job of the provider's module. Design:
[ADR-0024](../adr/0024-inbound-webhooks-envelope-encryption-card-data.md);
URL handshakes and platform-owned accounts:
[ADR-0038](../adr/0038-entitlements-platform-accounts-mailer.md).

## Public API (`internal/modules/webhooks/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the public route (no session) |
| `Module.Register(Provider)` | Adds a provider at startup; names are unique |
| `Module.RegisterPlatform(PlatformProvider)` | Adds a provider's platform-owned account (one WhatsApp number for all stores) |
| `Challenger` | Optional on a `Provider`: `Challenge` answers the GET URL handshake (Meta's `hub.challenge`) |
| `PlatformProvider` | `Name`, `VerifyPlatform` (signature → one `Part` per store event), `ChallengePlatform`, `AcceptTx` |
| `URL`, `PlatformURL` | Where a provider sends a connection's, or the platform account's, webhooks |
| `Module.Inbox() Inbox` | What processing jobs use: `Get` (receipt + raw body), `MarkProcessedTx` (first outcome wins) |
| `Provider` | `Name`, `Verify` (signature → event id), `AcceptTx` (enqueue the job in the receipt's transaction) |
| `Request`, `Verified`, `Receipt`, `Part`, `Limits`, `MaxBody` | DTOs and limits |
| `ErrForged`, `ErrUnknownConnection` | What `Verify` and `Challenge` return |
| `ErrUnknownStore` | A platform part whose store does not exist; it is dropped |
| `ErrReceiptNotFound` | `Get` on a missing receipt |

## Writing a provider

1. Store the connection's verification secret sealed with
   `platform/envelope`, AAD `<module>:<provider>:<store>:<connection>`.
2. `Verify` looks the connection up **in the store in ctx** (RLS), then
   checks the provider's signature with a constant-time comparison. It
   returns `ErrUnknownConnection` (404) or `ErrForged` (401). Any other
   error means it could not decide, and the provider will retry. On
   success it returns the provider's event id: stable across the
   provider's retries, and different for each real change.
3. `AcceptTx` inserts the module's River job with the receipt ID.
4. The job reads the body with `Inbox.Get`, **re-queries the provider**,
   changes state from that answer, and calls `MarkProcessedTx` in the
   same transaction.

## Platform-owned accounts

Some accounts belong to the platform and serve every store: the shared
WhatsApp number (ADR-0038). Their provider gets one URL without a store,
`/v1/webhooks/<provider>`.

1. `VerifyPlatform` checks the whole delivery against the platform's
   secret, then splits it into `Part`s: store, connection, the event's
   id and the event alone as the body. It places each event from data
   the platform signed (for WhatsApp, our message ID echoed back) and
   drops, with a log line, what it cannot place.
2. Each part is accepted in its own store's transaction, exactly like a
   connection's delivery: once per event id, with its body and job.
3. A failure part-way fails the delivery. The provider retries it, and
   the parts already accepted are replays. A part whose store no longer
   exists is dropped.

## HTTP API (tag `webhooks`)

| Operation | Notes |
|---|---|
| `receiveWebhook` `POST /v1/webhooks/{provider}/{store_id}/{connection_id}` | 200 `accepted` or `duplicate`; 401 forged; 404 unknown provider, store or connection; 400 over 64 KiB or no event id; 429 over the limits |
| `verifyWebhook` `GET` same path | The URL handshake: 200 echoes `hub.challenge` as `text/plain` (nosniff, no-store); 401 wrong token; 404 no handshake, unknown store or connection; 400 a challenge that is not 1-200 URL-safe characters |
| `receivePlatformWebhook` `POST /v1/webhooks/{provider}` | As `receiveWebhook`, for a platform-owned account, with up to 2 MiB and 1,000 events (Meta batches 1,000 updates); `duplicate` only when every placed event was a replay |
| `verifyPlatformWebhook` `GET /v1/webhooks/{provider}` | The handshake against the platform's token |

Limits: 1,200/min per client IP, 600/min per connection, 6,000/min per
platform-owned account.

## Tables

Both are tenant tables under RLS.

| Table | Notes |
|---|---|
| `webhook_receipts` | Unique `(store_id, provider, event_id)`, the once-only gate. Kept 90 days, purged per store as new receipts arrive. |
| `webhook_bodies` | Raw bodies, append-only. Partitioned by month on the receipt's ULID time; partitions dropped after 3 whole months. |

## Rules worth knowing

- **Forged deliveries store nothing.** The log line `webhook rejected`
  carries the reason, the network prefix and the body's SHA-256.
- **Testing:**
  - `internal/webhooktest` is an HMAC provider for this module's tests.
    It also has a URL handshake, and a platform-owned account
    (`webhooktest.Platform`) that splits `{"events":[…]}` by store.
  - The tests cover five replays (sequential and concurrent), forged
    deliveries and their logs, cross-tenant connections and receipts,
    rollback when `AcceptTx` fails, retention and rate limits. They also
    cover handshakes (wrong token, cross-tenant, unsafe challenges), and
    platform deliveries split across two stores, replayed, forged, failing
    part-way, or naming a store that is gone.
- **Fuzzing.** `domain`: `FuzzValidEventID`, `FuzzValidChallenge`. Each provider's parser has
  its own fuzz test in its module.
