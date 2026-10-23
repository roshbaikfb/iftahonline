# Module: webhooks

The webhooks module receives **inbound webhooks** from carriers and
payment gateways: it verifies each delivery through its provider, stores
the raw body, and lets each provider event through **once**, to a
processing job of the provider's module. Design:
[ADR-0024](../adr/0024-inbound-webhooks-envelope-encryption-card-data.md).

## Public API (`internal/modules/webhooks/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the public route (no session) |
| `Module.Register(Provider)` | Adds a provider at startup; names are unique |
| `Module.Inbox() Inbox` | What processing jobs use: `Get` (receipt + raw body), `MarkProcessedTx` (first outcome wins) |
| `Provider` | `Name`, `Verify` (signature → event id), `AcceptTx` (enqueue the job in the receipt's transaction) |
| `Request`, `Verified`, `Receipt`, `Limits`, `MaxBody` | DTOs and limits |
| `ErrForged`, `ErrUnknownConnection` | What `Verify` returns |
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

## HTTP API (tag `webhooks`)

| Operation | Notes |
|---|---|
| `receiveWebhook` `POST /v1/webhooks/{provider}/{store_id}/{connection_id}` | 200 `accepted` or `duplicate`; 401 forged; 404 unknown provider, store or connection; 400 over 64 KiB or no event id; 429 over the limits |

Limits: 1,200/min per client IP, 600/min per connection.

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
  - The tests cover five replays (sequential and concurrent), forged
    deliveries and their logs, cross-tenant connections and receipts,
    rollback when `AcceptTx` fails, retention and rate limits.
- **Fuzzing.** `domain`: `FuzzValidEventID`. Each provider's parser has
  its own fuzz test in its module.
