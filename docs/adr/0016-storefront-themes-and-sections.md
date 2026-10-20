# ADR-0016: Storefront themes and pages built from schema-validated sections

- Status: Accepted
- Date: 2026-10-20

## Context

Merchants must make their store look like theirs: colors, font, a home
page, campaign landing pages (the main sales tool in Egypt and the Gulf)
and policies. The usual shortcut, letting merchants paste HTML or
scripts, breaks everything else we promise:

- the strict CSP;
- XSS safety across 5,000 stores on shared origins;
- edge caching;
- the 1.5 s LCP budget.

A page builder must therefore be expressive enough to sell, yet closed:
every byte a shopper's browser runs comes from us.

Two editors (owner and manager) may also edit the same page at the same
time from the dashboard and the mobile app.

## Decision

1. **A page is an ordered list of sections:** `{id, type, settings}`.
   There are seven types:
   - `hero`
   - `product_grid` (newest, a category, or hand-picked)
   - `product_landing` (one product, big)
   - `reviews` (testimonials the merchant enters; real customer reviews
     come with orders)
   - `faq`
   - `trust_badges` (from a fixed icon set)
   - `countdown`

   There is no HTML, script, style or iframe type, and none will be
   added.
2. **Each type's settings have a JSON Schema (2020-12),** embedded in the
   binary (`storefront/internal/sections/schemas`).
   - The same schema is served to the dashboard editor
     (`listSectionTypes`) and enforced on every save
     (`santhosh-tekuri/jsonschema/v6`, write path only, never on render).
   - The schemas are closed (`additionalProperties: false`).
   - Text fields are length-bounded single lines unless they are meant
     to hold paragraphs.
   - Links are store paths (`/…`, not `//…`) or `https://` URLs only.
     That rules out `javascript:` and `data:`.
   - Every settings value is also decoded into the renderer's Go type
     before it is stored, because JSON Schema accepts `8.0` as an
     integer and RFC 3339 forms that Go's time parser rejects. Stored
     settings therefore always render.
   - Errors name the exact field: `sections[2].settings.items[0].rating`.
3. **Pages:**
   - **Home:** one per store. It is the default (hero, newest products,
     trust badges) until saved.
   - **Landing:** up to 50 per store, with a slug that follows the
     product slug rules (`platform/slug`), draft or published.
   - **Policy:** shipping, refund, privacy and terms. Each is a
     plain-text body of paragraphs, shown only once published.
   - Defaults carry empty texts that the renderer fills in the shopper's
     language, so a new store reads right in Arabic and English.
   - Limits: 30 sections, 64 KiB of sections JSON, 200-character title,
     300-character meta description, 20,000-character policy body.
4. **Theme:**
   - Four colors (`#rrggbb`).
   - A font from a self-hosted list (Cairo, Tajawal, IBM Plex Sans
     Arabic, Noto Kufi Arabic, or the device font).
   - A corner radius.
   - Body text must contrast with the background at least 4.5:1 (WCAG
     AA). Button text color is derived (`OnColor`), not chosen.
   - Colors are validated as hex, so they cannot smuggle CSS into the
     inlined stylesheet.
   - The logo comes in a follow-up change, together with moving the
     image pipeline to `platform/media`.
5. **Optimistic concurrency:**
   - Every save sends the `version` it read: 0 to create.
   - A stale version, or a create that lost a race, is 409
     `version_conflict`.
   - Nothing is overwritten silently.
6. **Ownership and events:**
   - The `storefront` module owns `store_themes` and `store_pages`
     (RLS; `store_id` leads every index).
   - Slugs are `text`, for index use under RLS (ADR-0015 §4).
   - Saves publish `storefront.theme.changed` or
     `storefront.page.changed` through the event bus, with an audit
     entry in the same transaction. The page event includes the previous
     slug when a landing page moves. Renderer caches subscribe to these
     (P3 rendering).
7. **Authorization:** one permission, `storefront.manage`, for owners and
   managers. Support and shipping staff do not change how the store
   looks.

## Consequences

- **Positive:**
  - No merchant input can execute in a shopper's browser. The CSP stays
    `script-src 'self'`.
  - The editor and the server agree on validity because they read the
    same schema.
  - Concurrent editors get a conflict instead of lost work.
  - Pages render from a bounded, typed structure, which keeps server
    render time predictable.
- **Negative:**
  - Merchants who want a layout we do not offer must wait for a new
    section type. Adding one means a schema, a Go type, a templ
    component and an enum value in `api/openapi.yaml`.
  - Testimonials are self-declared until verified reviews exist.
  - A schema change must stay compatible with stored settings (expand
    then contract), like a database migration.
