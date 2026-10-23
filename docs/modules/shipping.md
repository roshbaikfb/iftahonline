# Module: shipping

The shipping module owns **delivery zones and rates**, **free shipping**
and the **cash-on-delivery fee**, and quotes what delivering an order
costs. Design: [ADR-0025](../adr/0025-shipping-zones-rates-cod-fee.md).
Carriers and COD reconciliation join it in P5c and P5d.

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

## Events

| Event | Published when |
|---|---|
| `shipping.rates.changed` | The settings or a zone is saved or deleted |

## Tables

All are tenant tables under RLS.

| Table | Notes |
|---|---|
| `shipping_settings` | One row per store once saved (`version`) |
| `shipping_zones` | Name, rate, optional threshold and COD fee (all four fee columns or none), position, version |
| `shipping_zone_areas` | Primary key `(store_id, area)`: one zone per governorate |
| `shipping_city_rates` | `(store_id, zone_id, city_key)`; the name as typed is kept for display |

## HTTP API (tag `shipping`)

| Operation | Permission |
|---|---|
| `getShipping` (currency, settings, zones) | shipping.read |
| `saveShippingSettings` (`version`, 0 the first time) | shipping.manage |
| `createShippingZone` | shipping.manage |
| `updateShippingZone` (`version` required) | shipping.manage |
| `deleteShippingZone` | shipping.manage |
| `quoteShipping` (governorate, city, subtotal) | shipping.read |

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
- **Benchmark:** `BenchmarkQuote`, about 0.6 µs/op.
- **Fuzzing.** `domain`: `FuzzQuote`.
