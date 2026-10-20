-- Variants are written in batches: one statement per kind of change, not
-- one per variant. Batches are parallel arrays indexed by
-- generate_subscripts (sqlc cannot type a multi-argument unnest), and
-- option_values travel as JSON arrays because PostgreSQL cannot index a
-- text[][] into rows of text[].

-- name: InsertVariants :exec
INSERT INTO variants (id, store_id, product_id, sku, option_values, price_minor, compare_at_minor, currency, position)
SELECT (@ids::uuid[])[n], @store_id, @product_id, nullif((@skus::text[])[n], ''),
       ARRAY(SELECT jsonb_array_elements_text((@option_values::jsonb[])[n])),
       (@prices::bigint[])[n], nullif((@compare_ats::bigint[])[n], 0), @currency, (@positions::int[])[n]
FROM generate_subscripts(@ids::uuid[], 1) AS n;

-- name: InsertInventoryLevels :exec
INSERT INTO inventory_levels (variant_id, store_id, tracked, on_hand)
SELECT (@variant_ids::uuid[])[n], @store_id, (@tracked::boolean[])[n], (@on_hand::int[])[n]
FROM generate_subscripts(@variant_ids::uuid[], 1) AS n;

-- name: UpdateVariants :execrows
UPDATE variants AS t
SET sku = nullif((@skus::text[])[v.n], ''),
    option_values = ARRAY(SELECT jsonb_array_elements_text((@option_values::jsonb[])[v.n])),
    price_minor = (@prices::bigint[])[v.n], compare_at_minor = nullif((@compare_ats::bigint[])[v.n], 0),
    position = (@positions::int[])[v.n], updated_at = now()
FROM (SELECT n, (@ids::uuid[])[n] AS id FROM generate_subscripts(@ids::uuid[], 1) AS n) AS v
WHERE t.store_id = @store_id AND t.product_id = @product_id AND t.id = v.id AND t.deleted_at IS NULL;

-- name: SoftDeleteVariantsExcept :exec
-- Deletes the product's live variants whose ID is not in keep. Their SKU
-- and option combination become free (the unique indexes are partial).
UPDATE variants SET deleted_at = now(), updated_at = now()
WHERE store_id = @store_id AND product_id = @product_id AND deleted_at IS NULL
  AND NOT (id = ANY(@keep::uuid[]));

-- name: ListProductVariantIDs :many
SELECT id FROM variants
WHERE store_id = @store_id AND product_id = @product_id AND deleted_at IS NULL;

-- name: ListVariantsByProducts :many
-- One index lookup per product: the LIMIT (= domain.MaxVariants) keeps
-- the planner from flattening this into a store-wide scan.
SELECT v.id, v.product_id, v.sku, v.option_values, v.price_minor, v.compare_at_minor, v.currency, v.position,
       v.tracked, v.on_hand, v.reserved
FROM unnest(@product_ids::uuid[]) WITH ORDINALITY AS ids(product_id, ord)
CROSS JOIN LATERAL (
  SELECT v.id, v.product_id, v.sku, v.option_values, v.price_minor, v.compare_at_minor, v.currency, v.position,
         i.tracked, i.on_hand, i.reserved
  FROM variants v
  JOIN inventory_levels i ON i.store_id = v.store_id AND i.variant_id = v.id
  WHERE v.store_id = @store_id AND v.product_id = ids.product_id AND v.deleted_at IS NULL
  ORDER BY v.position, v.id
  LIMIT 100) v
ORDER BY ids.ord, v.position, v.id;

-- name: GetSellableVariant :one
SELECT v.id, v.product_id, p.title AS product_title, p.slug AS product_slug, v.sku, v.option_values,
       v.price_minor, v.compare_at_minor, v.currency, i.tracked, i.on_hand, i.reserved
FROM variants v
JOIN products p ON p.store_id = v.store_id AND p.id = v.product_id
JOIN inventory_levels i ON i.store_id = v.store_id AND i.variant_id = v.id
WHERE v.store_id = @store_id AND v.id = @id AND v.deleted_at IS NULL
  AND p.deleted_at IS NULL AND p.status = 'active';

-- name: ListSellableVariants :many
-- GetSellableVariant for many IDs (a cart), one primary-key lookup per
-- ID in the order given; IDs that are not sellable are skipped.
SELECT v.id, v.product_id, p.title AS product_title, p.slug AS product_slug, v.sku, v.option_values,
       v.price_minor, v.compare_at_minor, v.currency, i.tracked, i.on_hand, i.reserved
FROM unnest(@ids::uuid[]) WITH ORDINALITY AS ids(id, ord)
JOIN variants v ON v.id = ids.id AND v.store_id = @store_id AND v.deleted_at IS NULL
JOIN products p ON p.store_id = v.store_id AND p.id = v.product_id
JOIN inventory_levels i ON i.store_id = v.store_id AND i.variant_id = v.id
WHERE p.deleted_at IS NULL AND p.status = 'active'
ORDER BY ids.ord;
