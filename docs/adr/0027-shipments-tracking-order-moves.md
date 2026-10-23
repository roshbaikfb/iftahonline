# ADR-0027: Shipments: created by jobs, tracked by webhooks and polls, moving their orders

- Status: Accepted
- Date: 2026-10-23

## Context

ADR-0026 gave us carriers and merchants' connections to them. P5 still
asks for:

- **Tracking** via verified webhooks, with a polling fallback.
- **Webhooks processed once.** State changes only after re-querying the
  provider (ADR-0024). Replaying a webhook five times changes state
  exactly once.
- **The order follows the parcel.** Picked up is shipped; then delivered,
  a failed attempt, or returned (ADR-0021's state machine).

Constraints:

- Side effects are jobs in the transaction that causes them (CLAUDE.md
  rule 9). No external call in a request path that can be a job.
- Modules talk through api.go only. But orders already imports shipping
  (to price deliveries, ADR-0025), so shipping cannot import orders.
- FORCE RLS binds even the table owner, so a global "every store's due
  shipment" query would see nothing (ADR-0021).

## Decision

1. **Shipments** (migration 00032):
   - **Starting.** A merchant hands up to 100 orders, each
     `ready_to_ship`, to a connected carrier (`createShipments`, one
     result per order). Each gets a `requested` shipment and its create
     job in one transaction. The carrier is called by the job, not
     while the merchant waits.
   - **One live shipment per order**, by a partial unique index. A
     `failed` or `cancelled` shipment frees the order.
   - **A tracking number is unique per store and carrier**, which is
     how a webhook finds its shipment.
   - **The timeline** (`shipment_events`) is append-only, partitioned
     by month, and records the source of each change: api, webhook,
     poll or merchant.
2. **Jobs** (River, tenant-scoped):
   - **create_shipment.** Creates the parcel with the shipment's ID as
     the reference (idempotent, ADR-0026), records the tracking number,
     enqueues `fetch_label`, and schedules `track_shipment`. A carrier
     refusal (`ErrRejected`/`ErrAuth`) fails the shipment with the
     carrier's reason; anything else is retried. A shipment cancelled
     meanwhile stays cancelled.
   - **fetch_label** stores the PDF under the store's object prefix.
     `getShipmentLabel` serves it, 409 `label_not_ready` until then.
   - **cancel_shipment.**
     - Before the carrier knows the parcel, cancelling is immediate.
     - After, the job asks the carrier. A carrier that refuses (the
       parcel is picked up) leaves a note on the timeline.
   - **track_shipment, the polling fallback.** One job per shipment
     polls the carrier and then snoozes `PollEvery` (6 h) until the
     shipment is final.
     - A snooze uses no River attempt, so only a lasting outage spends
       attempts.
     - Scheduling per shipment avoids a cross-store query.
   - **process_webhook** reads the receipt's raw body, finds the
     shipment by tracking number in the receipt's store, then **tracks
     it at the carrier**. It applies what the carrier says, not what
     the webhook says, and records the receipt's outcome in the same
     transaction: updated, unchanged, unknown_shipment, disconnected or
     unreadable.
3. **One place changes a tracked shipment** (`applyTracking`), for
   webhooks, polls and cancels alike:
   - It locks the shipment.
   - A final shipment, an unknown carrier state, or the state it
     already has changes nothing; only the carrier's raw state is
     recorded.
   - Otherwise it moves the shipment, walks its order along, writes the
     timeline, and publishes `shipping.shipment.status_changed`, all in
     one transaction.
   - Five copies of one webhook therefore produce one receipt, and any
     second tracking of the same state is a no-op. The end-to-end test
     on the real modules and River proves it.
4. **The order follows the parcel**, by `domain.OrderPath`:
   - in transit or out for delivery → `shipped`;
   - delivered → `delivered`;
   - a failed attempt → `failed_delivery`;
   - returned → `returned`.

   An order that skipped states (a parcel already delivered at the first
   look) walks the path: shipped, then delivered.

   **How shipping moves orders.** It does so through a port
   (`shipping.Orders`), which cmd/platform adapts from the new
   `orders.Fulfilment`:
   - `TransitionSystemTx` makes the move in shipping's transaction, on
     behalf of `carrier:<name>`. It uses the state machine's edges and
     stock effects, without a person's permission.
   - Moving to the current status is a no-op.
   - The actor is recorded on the order's timeline (`order_events.actor`,
     migration 00031) and in the audit log.

   A move the state machine refuses (the merchant cancelled the order)
   leaves the order alone. The shipment still follows the parcel, and
   its timeline says the order was not moved.
5. **Webhooks.** Each carrier's provider registers on the inbox
   (ADR-0024):
   - **Verify** compares the connection's secret (`X-Webhook-Secret`,
     or `?secret=`) in constant time, then reads the event id.
   - **AcceptTx** enqueues `process_webhook` with the receipt.
6. **Roles:**
   - **The api role** serves the shipments API and the webhook endpoint.
   - **The worker role** runs the jobs, with the inbox and
     `orders.NewFulfilment`, an orders service without merchant
     permissions.
   - `media.NewS3For` builds the object store, replacing three copies.
   - `Module.FakeCourier` moves the fake carrier's parcels, for
     development and end-to-end tests (only when `SHIPPING_FAKE_CARRIER`
     is on).

## Consequences

- **Positive:**
  - A lost, late, repeated or forged webhook can neither skip nor
    double a state change. Polls catch what webhooks miss, within
    6 hours.
  - The order's timeline shows who moved it: a person, or the carrier.
  - The merchant never waits on a carrier's API.
- **Negative:**
  - **A shipment takes seconds to get its tracking number and label**
    (the jobs). The API answers `requested` first.
  - **A lost webhook costs up to 6 hours** before the poll sees the
    change.
  - **Each active shipment has a snoozing River job.** With 100,000
    parcels on the road, that is 100,000 rows and about 5 carrier calls
    a second.
  - **The fake carrier's parcels live in process memory.** In
    development, the worker that created a parcel is the one that
    tracks it.
