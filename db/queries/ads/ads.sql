-- Ads in free stores (ADR-0062).

-- name: GetSettings :one
SELECT adsense_enabled, publisher_id, units FROM ad_settings WHERE id = 1;

-- name: SaveSettings :exec
UPDATE ad_settings SET adsense_enabled = @adsense_enabled, publisher_id = @publisher_id, units = @units, updated_at = now() WHERE id = 1;

-- name: LiveBanners :many
-- The banners on show at now in country (or every country), by placement.
SELECT id, title, image_key, width, height, link_url, placement, weight
FROM ad_banners
WHERE active AND starts_at <= @now AND (ends_at IS NULL OR ends_at > @now)
  AND (country IS NULL OR country = @country)
ORDER BY placement, id
LIMIT 200;

-- name: GetLiveBanner :one
SELECT link_url FROM ad_banners
WHERE id = @id AND active AND starts_at <= @now AND (ends_at IS NULL OR ends_at > @now);

-- name: CountClick :exec
INSERT INTO ad_clicks (banner_id, day, clicks) VALUES (@banner_id, @day, 1)
ON CONFLICT (banner_id, day) DO UPDATE SET clicks = ad_clicks.clicks + 1;

-- name: ListBanners :many
-- Every banner for the console, newest first, with its clicks since since.
SELECT b.id, b.title, b.image_key, b.width, b.height, b.link_url, b.placement, coalesce(b.country::text, '')::text AS country, b.starts_at, b.ends_at,
       b.weight, b.active, coalesce((SELECT sum(c.clicks) FROM ad_clicks c WHERE c.banner_id = b.id AND c.day >= @since), 0)::bigint AS clicks
FROM ad_banners b
ORDER BY b.created_at DESC
LIMIT 500;

-- name: InsertBanner :exec
INSERT INTO ad_banners (id, title, image_key, width, height, link_url, placement, country, starts_at, ends_at, weight)
VALUES (@id, @title, @image_key, @width, @height, @link_url, @placement, @country, @starts_at, @ends_at, @weight);

-- name: SetBannerActive :execrows
UPDATE ad_banners SET active = @active, updated_at = now() WHERE id = @id;
