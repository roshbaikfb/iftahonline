# ADR-0036: Storefront drafts, preview links, and checkout's starting method

- Status: Accepted
- Date: 2026-10-28

## Context

Until now every save of the theme or the home page went live at once.
The owner approved U6 (2026-10-28): edits are kept as a draft, the
merchant previews the shop with them, and only «نشر» shows them to
shoppers. The approved mockups also have «تجاهل التغييرات» to drop a
draft. The same plan lets the merchant choose whether checkout starts on
card (cash on delivery stays the default).

The storefront's CSP sends `frame-ancestors 'none'`, and its pages are
cached in memory and at the edge by URL. A preview must not weaken
either, and must never reach a shopper or a search engine.

## Decision

1. **Drafts in their own table.** `storefront_drafts` holds one row per
   store, with nullable `theme` (ThemeSettings) and `home` (title,
   description, sections) parts. Drafts are validated exactly as the
   published versions are. Saving a draft publishes no event and
   changes no page. The live `store_themes` and `store_pages` rows stay
   the only source for shoppers.
2. **Publishing is the old save, in one transaction with the draft.**
   `publishThemeDraft` and `publishHomeDraft` take the published version
   the merchant read. They save over it with the existing optimistic
   check (409 `version_conflict`) and clear the draft part. They publish
   the usual `theme.changed` and `page.changed` events. A draft is
   validated again when published, since the rules may have tightened.
   Without a draft the call is 409 `no_draft`. The old direct-save
   endpoints stay (within /v1).
3. **Preview links, not frames.** `createStorefrontPreview` returns a
   random 256-bit token, valid 30 minutes, stored only as its SHA-256 in
   `storefront_previews`, with at most 20 live links per store. The shop
   address plus `?preview=<token>` renders:
   - the drafts (missing parts fall back to the published ones);
   - a «معاينة» banner, styled by the page's own hashed style, so
     shoppers' pages carry none of it;
   - links that keep the token;
   - `Cache-Control: private, no-store`, `X-Robots-Tag: noindex` and a
     robots meta.

   Such a response is never written to the page cache. An unknown or
   expired token shows the published shop, as if absent. The token is
   checked against the store the host resolved, so another store's
   token opens nothing. The dashboard opens the link in a new tab; the
   storefront's `frame-ancestors 'none'` stays.
4. **Checkout's starting method lives in the payments module.**
   `payment_settings.default_method` is `cod` (default) or `card`.
   `CardCheck.CardDefault` is true only when card is chosen *and* a
   gateway is connected. The shopper can always switch. Changing it
   publishes `payments.settings.changed`, which drops the store's cached
   pages like a gateway change does.

## Consequences

- One extra small table read per preview request. Shoppers' requests
  are unchanged.
- A preview is visible to whoever holds the link for 30 minutes. It
  shows only what the merchant is about to publish, is not indexed,
  and is not cached.
- Landing pages and policies keep their existing draft/published status;
  only the theme and home page use draft rows.
