# ADR-0035: Size charts and option colours

- Status: Accepted
- Date: 2026-10-27

## Context

The storefront's product page (U2b, approved mockup) has a «جدول
المقاسات» section that stayed hidden: products had no size chart. Colour
swatches were guessed from the value's name (a fixed list of common
Arabic and English names), and a name outside it showed as a text
button. The owner asked for both in U5 (2026-10-27), choosing:

1. size charts that a store keeps and reuses across products;
2. centimetres only, with column names the merchant writes;
3. a colour per option value, set with a picker, falling back to the
   name's guess.

Two constraints shape the design. The storefront's CSP allows no inline
style attribute (`style-src 'self'` plus the hash of the store's one
inline stylesheet), so a merchant's colour cannot be written as
`style="background:…"`. And a chart is shared, so one edit changes many
cached pages.

## Decision

1. **A `size_charts` table, owned by the catalog.** A chart has the
   merchant's `name` (never shown to shoppers), a `header` of 2–8 column
   names (the first is the size) and a `body` of 1–30 rows, each as long
   as the header, as JSONB. Cells are text up to 30 characters: the
   measurement's form ("96", "96–100") is the merchant's. The first cell
   of a row is required; the others may be blank. At most 100 charts per
   store. The unit is centimetres, stated by the page, not stored.
2. **`products.size_chart_id`**, nullable, with a composite foreign key
   `(store_id, size_chart_id)`, so a product can only point at its own
   store's chart (a foreign or unknown ID is a 422 on `size_chart_id`).
   Deleting a chart sets the column NULL (`ON DELETE SET NULL
   (size_chart_id)`, PostgreSQL 15+): its products simply lose it.
3. **Colours live on the option**, in the product's `options` JSONB:
   `colors` is absent (not a colour option) or holds one entry per value,
   `#rrggbb` (stored lower case) or `""` (guess from the name). No
   migration: old products have no `colors` and behave as before. An
   option with `colors` shows swatches whatever its name.
4. **The page's own stylesheet carries the colours.** The product page
   gives each set colour a class (`swc-<option>-<value>`) and writes
   `.swc-1-0{background:#1f2a44}` into a second inline `<style>`; the
   page's CSP adds that style's hash beside the store's. Only a value
   that is exactly `#rrggbb` is written, checked when saved and again
   when rendered.
5. **One event per chart change.** Replacing or deleting a chart
   publishes `catalog.size_chart.changed`. The storefront treats it like
   a category change: it drops the store's cached pages and purges the
   store's edge tag. Charts change rarely, and listing every product
   that uses one in the event (or one `product.changed` each) would be
   unbounded.
6. **Only the product page reads the chart**: `ProductBySlug` makes one
   more query when the product has one; lists and search never do.
7. **A variant can be hidden** (`variants.hidden`, migration 00041; the
   owner's choice, 2026-10-27, over deleting it). Unticking «متاح للبيع»
   in the editor hides it: it keeps its ID and stock, but it is not
   sellable, not reserved, not listed as running low and not shown to
   shoppers. A product with every variant hidden leaves the shop. Ticking
   it again puts it back on sale with the stock it had.

## Consequences

- A merchant edits a chart once for all its products; the dashboard lists
  how many products use each (one counted subquery per chart, on the
  partial index `products_size_chart`).
- A second inline style and hash on product pages with merchant colours;
  pages without them are unchanged.
- A chart edit re-renders the whole store's pages, not only its
  products'. Acceptable for a rare action; revisit if merchants edit
  charts often.
- Only centimetres. Inches would need a unit per chart (or a conversion)
  and are not asked for in Egypt and the Gulf.
