-- Product and category slugs become text (ADR-0015).
--
-- Under row level security PostgreSQL only uses LEAKPROOF operators in an
-- index condition. citext's = is not leakproof, so app_user's slug
-- lookups could not use the (store_id, slug) indexes and filtered the
-- whole store row by row. Slugs are always lower-case (domain rules), so
-- citext bought nothing; text's = is leakproof. The CHECKs keep
-- uniqueness case-insensitive in effect.
--
-- citext -> text is binary coercible: no table rewrite, only the slug
-- indexes are rebuilt. Running binaries keep working (their parameters
-- compare as text).

-- +goose Up
ALTER TABLE products ALTER COLUMN slug TYPE text;
ALTER TABLE products ADD CONSTRAINT products_slug_lower CHECK (slug = lower(slug));
ALTER TABLE categories ALTER COLUMN slug TYPE text;
ALTER TABLE categories ADD CONSTRAINT categories_slug_lower CHECK (slug = lower(slug));

-- +goose Down
ALTER TABLE categories DROP CONSTRAINT categories_slug_lower;
ALTER TABLE categories ALTER COLUMN slug TYPE public.citext;
ALTER TABLE products DROP CONSTRAINT products_slug_lower;
ALTER TABLE products ALTER COLUMN slug TYPE public.citext;
