# Module: ads

The ads module serves **ads in free stores** (ADR-0062). It shows the
platform's own banners first, then Google AdSense, in four placements.
It owns only platform data: AdSense's settings, the banners and their
daily clicks. Whether a store shows ads is not this module's decision.
The storefront asks entitlements whether the store has `ad_free`.

## Public API (`internal/modules/ads/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)` | Wiring. `Pool` (app_user) is required. `AdminPool` and `Media` are for the console (the api role) |
| `Module.Shop()`, `Shop` | The storefront's side. `Slots(country)` gives what each placement shows, cached a minute per country. `PublisherID`, `AdsTxt`, and `Click(id)`, which counts today's click and returns the link |
| `Module.Console()`, `Console` | The console's side. `Settings` and `SaveSettings`, `Banners` (with the last 30 days' clicks), `UploadURL`, `CreateBanner` (re-encodes the upload), `SetActive`, `Thumbnail` |
| `Placement`, `HomeTop`, `Grid`, `CategoryTop`, `ProductBottom`, `Placements()` | The placements |
| `Slot`, `Banner`, `Settings`, `Upload` | DTOs |

## Events

None emitted or consumed. Changes reach pages through the one-minute
cache and the page caches' lifetime (approved: no mass purge).

## Tables (migration 00068)

| Table | Notes |
|---|---|
| `ad_settings` | One row (`id = 1`): `adsense_enabled`, `publisher_id` (`ca-pub-` and 16 digits), `units` (jsonb, placement → 10-digit unit) |
| `ad_banners` | `image_key` under `platform/ads/`, `link_url` https only, `placement`, `country` (NULL = every country), `starts_at` / `ends_at`, `weight` 1–100, `active`. Index `ad_banners_live (placement, starts_at) WHERE active` |
| `ad_clicks` | `(banner_id, day)` primary key, `clicks` upserted +1 |

Grants: `app_user` reads the settings and banners and inserts or updates
clicks. `app_admin` updates the settings, inserts and updates banners,
and reads clicks. Nobody deletes.

## Where ads show (storefront)

- **Pages:**
  - home: `home_top` under its first section, `grid` under its second;
  - category: `category_top`, and `grid` after the fourth card when more
    follow;
  - product: `product_bottom`.
- **Never:** the cart, checkout, orders, tracking, thank-you, landing
  pages and previews.
- **Paid stores:** a store with `ad_free` shows none and answers 404 at
  `/ads.txt` and `/ad/{id}`.
- **Script and CSP:** `ads.js` and AdSense's CSP origins are added only
  to pages that show a unit.
