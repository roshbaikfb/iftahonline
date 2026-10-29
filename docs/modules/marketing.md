# Module: marketing

The marketing module owns a store's **ad pixels** and the shopper's
**consent**:
- which ad and analytics providers its pages measure with, and their
  public IDs;
- the sealed Conversions API tokens;
- when pages may load the pixels.

The storefront renders them through `Pixels`. Design:
[ADR-0043](../adr/0043-ad-pixels-and-conversions.md).

**Status:**
- 6e-1 (this): the pixels, the consent mode and their API.
- 6e-2: the storefront's tags (`px.js`, CSP per store, consent banner),
  UTM and the order's signals.
- 6e-3: the server-side conversions.

## Public API (`internal/modules/marketing/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring; the merchant HTTP API (behind identity's membership middleware) |
| `Module.Pixels()` → `Pixels` | What the storefront uses: `Browser(ctx)`, which returns the enabled pixels' public IDs, the consent mode, and the CSP origins they need. It never reads a token |
| `EventPixelsChanged`, `PixelsChanged` | `marketing.pixels.changed`: a pixel saved or removed, or the consent mode changed |
| `Provider`, `Consent`, `Browser`, `BrowserPixel` | DTOs |

## Providers

The list is closed. Each provider is a `Spec` in
`internal/domain/pixel.go`. A spec gives:
- the ID pattern;
- whether a label or a test code is needed or taken;
- whether the provider has a server API;
- the CSP origins of its browser tag.

| Provider | ID | Also | Server API |
|---|---|---|---|
| `meta` | digits (10–20) | test code | Conversions API (token) |
| `tiktok` | code, `A-Z0-9` (15–25) | test code | Events API (token) |
| `snap` | UUID | | Conversions API (token) |
| `google_ads` | `AW-…` | purchase conversion label | none yet |
| `ga4` | `G-…` | | Measurement Protocol (API secret) |

**Tokens:**
- They are sealed with `platform/envelope`. The associated data is
  `marketing:<provider>:<store>:<row id>`.
- They are write-only: the API says `has_server_token`. Saving without
  `server_token` keeps the saved token, and `""` removes it.
- A provider without a server API cannot keep a token.

## Consent

| Mode | Pages ask first |
|---|---|
| `auto` (default) | where the store's country requires it (Saudi Arabia); the storefront decides |
| `required` | always |
| `off` | never |

## HTTP API (tag `marketing`)

| Operation | Notes |
|---|---|
| `listPixels` `GET /v1/stores/{store}/marketing/pixels` | Every provider's spec (`label`, `test_code`, `server`) and the store's pixels |
| `savePixel` `PUT …/marketing/pixels/{provider}` | Over the version read (0 to connect); 409 when it changed since; 404 for an unknown provider |
| `deletePixel` `DELETE …/marketing/pixels/{provider}` | Removes the pixel and its token |
| `getMarketingSettings`, `saveMarketingSettings` `…/marketing/settings` | The consent mode, over the version read |

All of them:
- need `marketing.manage` (owner, manager);
- publish `marketing.pixels.changed`, if they make a change;
- are audited (`pixel.saved`, `pixel.deleted`,
  `marketing_settings.updated`), without the token.

## Tables

| Table | Notes |
|---|---|
| `store_pixels` | Tenant table under RLS. PK `(store_id, provider)`. `server_token` is the sealed blob; `version` is used for optimistic locking |
| `marketing_settings` | Tenant table under RLS. One row per store, absent until the first save (`auto`) |

## Rules worth knowing

- **Testing:**
  - `domain` covers every provider's ID rules, labels, test codes and
    tokens, and the browser view (the registry's order, each origin
    once).
  - `app` runs on real PostgreSQL. It covers sealing (the blob opens
    only with its associated data, and never holds the token in the
    clear), keeping and removing tokens, stale versions, racing first
    saves, events and audit.
  - `httpapi` covers the happy path, validation, authorization, and a
    cross-tenant test for every operation.
