# Module: shipping

The shipping module owns **delivery zones and rates**, **free shipping**
and the **cash-on-delivery fee**, and quotes what delivering an order
costs, and connects stores to **carriers**. Design:
[ADR-0025](../adr/0025-shipping-zones-rates-cod-fee.md) (rates),
[ADR-0026](../adr/0026-carriers-bosta-connections.md) (carriers),
[ADR-0027](../adr/0027-shipments-tracking-order-moves.md) (shipments and
tracking), [ADR-0028](../adr/0028-cod-reconciliation.md) (COD
reconciliation).

## Public API (`internal/modules/shipping/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `Module.Quoter() Quoter` | `Quote(ctx, QuoteRequest) (Quote, error)` for the store in ctx; the orders module prices orders with it |
| `Module.Listen(ctx)` | Applies cache invalidations from Redis; run it in every role that quotes |
| `InvalidateHandler`, `InvalidateSubscriber`, `InvalidateChannel` | Subscribe the handler to `EventRatesChanged` on every role's bus |
| `QuoteRequest`, `Quote`, `RatesChanged`, `StoreInfo`, `StoreSource`, `CacheConfig` | DTOs; cmd/platform adapts tenancy to `StoreSource` |

## Rules

- **A zone** is governorates (ISO 3166-2, the store's country), each in
  one zone only. It has a flat rate, and optionally:
  - city rates (≤ 200), matched on the city's search form;
  - its own free-shipping threshold;
  - its own COD fee.
- **The store's settings** hold the default COD fee and the default
  free-shipping threshold.
- **A quote:**
  - without zones: delivers everywhere for 0, plus the COD fee;
  - otherwise the governorate's zone sets the rate, and a city rate
    overrides it;
  - at or above the threshold, shipping is free;
  - the COD fee is fixed plus basis points (≤ 1000) of the subtotal
    plus shipping, clamped to its minimum and maximum (0 = none);
  - a governorate outside every zone is not deliverable.

## Carriers

- **Adapters** (`internal/carrier`) meet one contract, the suite in
  `internal/carrier/carriertest`:
  - `fake`: in memory; for tests, and for development when
    `SHIPPING_FAKE_CARRIER=true`;
  - `bosta`: Egypt. It follows Bosta's SDKs and is unverified until the
    sandbox run (`BOSTA_SANDBOX_API_KEY=… go test ./internal/modules/shipping/internal/carrier/bosta/`).
- **Settlements.** Bosta has no settlements API: `CODSettlements` is
  `ErrUnsupported`.
- **Connections** keep the merchant's key envelope-sealed. The webhook
  secret is shown once and kept as a SHA-256. One active connection per
  carrier.

## Shipments

- **The lifecycle.** `requested` → `created` (tracking number, label) →
  `in_transit` → `out_for_delivery` → `delivered` | `failed_attempt` |
  `returned`; also `cancelled`, and `failed` when the carrier refuses
  the shipment. One live shipment per order.
- **Jobs:**
  - `shipping.create_shipment` and `shipping.fetch_label`;
  - `shipping.cancel_shipment`;
  - `shipping.track_shipment`: polls every 6 h until final, snoozing,
    for at most 60 days (`MaxTrackFor`; webhooks still move it);
  - `shipping.process_webhook`: re-queries the carrier.
- **Tracking.** Every change lands in `applyTracking`, which moves the
  order too: shipped, delivered, failed_delivery, returned, as
  `carrier:<name>`.
- **Ports.**
  - `Orders` is adapted from `orders.Fulfilment` in cmd/platform.
  - Webhook providers come from `Module.WebhookProviders()`.
  - The worker role needs `Inbox`, `Jobs` and `Objects`.

## COD reconciliation

- **Settlement lines** come from two sources:
  - a statement CSV the merchant uploads (`importCODStatement`);
  - a daily pull for carriers with a settlements API (Bosta has none).
- **Matching.** Lines are matched to shipments by tracking number.
  Re-importing adds nothing.
- **The report** (`getCODReport`) gives per carrier account: expected,
  settled, and held (aged 0–7, 8–14, 15+ days), the over-settled count,
  and the unmatched lines. `listHeldCOD` lists parcel by parcel.
- **The statement template:**
  - columns `tracking_number`, `amount`, then optionally `settled_at`
    (YYYY-MM-DD) and `reference`;
  - UTF-8, comma or semicolon, ≤ 1 MiB, ≤ 20,000 lines;
  - common Arabic and English column names are understood too.

## Events

| Event | Published when |
|---|---|
| `shipping.rates.changed` | The settings or a zone is saved or deleted |
| `shipping.shipment.status_changed` (`shipment_id`, `order_id`, `status`) | A carrier moves a shipment |

## Tables

All are tenant tables under RLS.

| Table | Notes |
|---|---|
| `shipping_settings` | One row per store once saved (`version`) |
| `shipping_zones` | Name, rate, optional threshold and COD fee (all four fee columns or none), position, version |
| `shipping_zone_areas` | Primary key `(store_id, area)`: one zone per governorate |
| `shipping_city_rates` | `(store_id, zone_id, city_key)`; the name as typed is kept for display |
| `carrier_connections` | Sealed credentials, the webhook secret's hash, `active`/`disconnected`; one active per carrier |
| `shipments` | One live per order; tracking number unique per store and carrier; COD to collect, label key, carrier's raw state |
| `shipment_events` | The timeline with its source; append-only, partitioned by month |
| `cod_settlements` | Payout lines; unique per connection, tracking number and reference; matched shipment or null |

## HTTP API (tag `shipping`)

| Operation | Permission |
|---|---|
| `getShipping` (currency, settings, zones) | shipping.read |
| `saveShippingSettings` (`version`, 0 the first time) | shipping.manage |
| `createShippingZone` | shipping.manage |
| `updateShippingZone` (`version` required) | shipping.manage |
| `deleteShippingZone` | shipping.manage |
| `quoteShipping` (governorate, city, subtotal) | shipping.read |
| `listCarrierConnections` (available carriers too; never credentials) | shipping.read |
| `connectCarrier` (checks the key; returns the webhook URL and secret once) | shipping.manage |
| `disconnectCarrier` | shipping.manage |
| `createShipments` (≤ 100 orders, one result each) | orders.fulfil |
| `listShipments` (order, status, cursor) | orders.read |
| `getShipment` (with its timeline) | orders.read |
| `cancelShipment` | orders.fulfil |
| `getShipmentLabel` (PDF) | orders.read |
| `getCODReport` (from, to) | finance.read |
| `listHeldCOD` | finance.read |
| `importCODStatement` (text/csv, `connection_id`) | finance.manage |

Roles: every role reads; the owner and manager change.

## Rules worth knowing

- **Caching.** Quotes come from an in-memory cache: 30 s TTL,
  invalidated through `shipping.rates.changed` → Redis. Merchant reads
  (`getShipping`) are never cached.
- **Orders.** Orders fix `shipping_minor` and `cod_fee_minor` when
  placed; the storefront shows the quote before submitting
  (`POST /order/quote`).
- **Testing:**
  - Config loads in a fixed number of queries (no N+1).
  - `TestConfigQueriesUseIndexesUnderRLS` checks every read uses its
    store_id index as app_user under generic plans.
  - Concurrent zones cannot share a governorate.
- **Acceptance.** `TestWebhookReplayedFiveTimesChangesStateOnce` runs
  the real modules and River.
- **Benchmark:** `BenchmarkQuote`, about 0.6 µs/op.
- **Fuzzing.** `domain`: `FuzzQuote`, `FuzzParseStatement`;
  `carrier/bosta`: `FuzzParseWebhook`.
