# ADR-0026: Carriers: one contract, a fake, Bosta, and sealed connections

- Status: Accepted
- Date: 2026-10-23

## Context

P5 asks for:

- a `Carrier` interface: CreateShipment, Label, Track, Cancel and
  CODSettlement;
- a first adapter (Bosta) and a FakeCarrier;
- one shared contract test suite run against the fake and the sandbox
  adapters;
- merchants' credentials encrypted at rest (ADR-0024).

What we found about Bosta before writing code:

- **No API reference.** Bosta publishes none we could check against;
  its developer portal shows only a welcome page.
  - Its own Node.js SDK gives the base URLs (`app.bosta.co`,
    `stg-app.bosta.co`) and the auth: the raw key in `Authorization`,
    no scheme.
  - A community .NET SDK gives the v2 paths, the delivery and webhook
    shapes, and the state codes.
- **No settlements API.** Cash settlements (the daily "collected cash
  cycle" and the weekly cash-out) exist only in Bosta's dashboard.
- **Unsigned webhooks.** Bosta's webhooks carry no signature.
- **No sandbox account yet.** We have none.

## Decision

1. **One contract for every courier** (`internal/carrier`):
   - **Operations:** `Verify` (check a key), `CreateShipment`, `Label`
     (PDF), `Track`, `Cancel`, `CODSettlements`, and `ParseWebhook`.
   - **Statuses.** Ours are created, in_transit, out_for_delivery,
     delivered, failed_attempt, returned, cancelled and unknown.
     Adapters map their carrier's states onto them; an unknown state
     moves nothing.
   - **Errors.** `ErrAuth`, `ErrRejected`, `ErrNotFound`,
     `ErrNotCancellable`, `ErrUnsupported` and `ErrBadWebhook` let jobs
     tell "retry" from "stop".
   - **Adapters are stateless.** Credentials come with each call. They
     print and log as `redacted`.
   - **`CreateShipment` is idempotent by our shipment ID** (the
     reference), because the job that calls it may run twice.
2. **One contract suite** (`internal/carrier/carriertest`) runs against:
   - the **fake carrier**: in memory, with test controls (Advance,
     Settle, Webhook);
   - **Bosta's in-process stand-in** (`bosta/bostatest`): it speaks the
     shapes above and can fail on demand;
   - **Bosta's staging API**, when `BOSTA_SANDBOX_API_KEY` is set.
     Parcels cannot be moved on a sandbox, so those cases are left out
     there.

   The stand-in proves the adapter speaks the shapes it was written
   for. Only the sandbox run can prove they are Bosta's.
3. **The Bosta adapter** follows the SDKs and is documented as unverified
   until the sandbox run:
   - **Creating.**
     - `POST /deliveries?apiVersion=1`, type 10 (send).
     - COD in pounds, EGP only.
     - The governorate as Bosta's city name: geo's English name, with
       three spellings overridden.
     - The shopper's city and address as the address lines.
     - The phone written the local way (`01…`).
     - Our shipment ID as `businessReference`.
   - **Idempotency.** Bosta takes no idempotency key, so every create
     first searches by `businessReference`. A retried job, or a create
     whose answer was lost, finds the first delivery instead of making a
     second (tested).
   - **The other calls:**
     - track: `GET /deliveries/business/{tracking}`;
     - label: `POST /deliveries/mass-awb`, an A6 base64 PDF;
     - cancel: `DELETE …/terminate`, checking the state first, so
       cancelling twice succeeds and a parcel already picked up is
       `ErrNotCancellable`.
   - **States:** 10/20/21 created; 22–25/30 in transit; 41/43 out for
     delivery; 45 delivered; 46/47 failed attempt; 48 cancelled; 49
     returned.
   - **Errors.** Auth failures and rejections are not retried; 429, 5xx
     and the network are, with backoff (`platform/retry`, 15 s per
     request).
   - **Settlements** are `ErrUnsupported`. COD reconciliation (P5d)
     imports the cash-out statement merchants download from Bosta's
     dashboard.
   - **Webhook event id.** It is tracking number + state + timestamp:
     stable across Bosta's retries of one change, different for each
     change. `FuzzParseWebhook` covers the parser.
4. **Connections** (`carrier_connections`, migration 00030):
   - **Checked before saving.** Connecting checks the key with the
     carrier, a 10 s call while the merchant waits, because a key that
     does not work must not be saved.
   - **Sealed.** The key is sealed with `platform/envelope`, with AAD
     `shipping:<carrier>:<store>:<connection>`. A blob copied into
     another store's row does not open (tested).
   - **The webhook secret** is 32 random bytes, shown to the merchant
     once, and stored only as its SHA-256. Deliveries present it in
     `X-Webhook-Secret`, or `?secret=` for carriers that allow no
     headers. It is compared in constant time.
   - Query strings are never written to access logs.
   - **One active connection per carrier** per store. A disconnected
     connection is kept for its past shipments, but its key is never
     opened again and its webhooks are refused.
   - **Webhook URL:** `<DASHBOARD_URL>/v1/webhooks/<carrier>/<store>/<connection>`.
     The dashboard and API share the public origin.
5. **Configuration:**
   - `BOSTA_BASE_URL` (default production);
   - `SHIPPING_FAKE_CARRIER` offers the fake carrier for development and
     demos, and is refused in production.

## Consequences

- **Positive:**
  - Adding a courier is an adapter and a harness, held to the same
    suite.
  - A Bosta outage or a retried job can neither double a delivery nor
    leak a key.
  - Merchants see a refused key at once, not when their first order
    fails to ship.
- **Negative:**
  - **Bosta is unverified until the sandbox run.** A shape we got wrong
    surfaces there, or with the first real merchant. The adapter keeps
    every Bosta detail in one file to make that fix small.
  - **One extra search per shipment** for idempotency.
  - **Settlements need a manual upload** for Bosta.
  - **The webhook secret cannot be shown again.** Rotating it is a
    reconnect until a rotate endpoint exists.
  - **The fake carrier keeps shipments in process memory.** In
    development, the worker that created a shipment is the one that
    tracks it.
