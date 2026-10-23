-- name: GetSettings :one
SELECT * FROM shipping_settings WHERE store_id = @store_id;

-- name: InsertSettings :one
-- First save (version 0 read): 0 rows when someone saved first.
INSERT INTO shipping_settings (store_id, cod_fixed_minor, cod_bp, cod_min_minor, cod_max_minor, free_over_minor)
VALUES (@store_id, @cod_fixed_minor, @cod_bp, @cod_min_minor, @cod_max_minor, sqlc.narg(free_over_minor))
ON CONFLICT (store_id) DO NOTHING
RETURNING *;

-- name: UpdateSettings :one
-- 0 rows when the version read is stale.
UPDATE shipping_settings
SET cod_fixed_minor = @cod_fixed_minor, cod_bp = @cod_bp, cod_min_minor = @cod_min_minor, cod_max_minor = @cod_max_minor,
    free_over_minor = sqlc.narg(free_over_minor), version = version + 1, updated_at = now()
WHERE store_id = @store_id AND version = @version
RETURNING *;

-- name: ListZones :many
-- A zone holds at least one governorate and a governorate one zone, so a
-- store has at most as many zones as its country has governorates (27).
SELECT * FROM shipping_zones WHERE store_id = @store_id ORDER BY position, id LIMIT 100;

-- name: ListZoneAreas :many
-- Every area of the store's zones in one query (no N+1).
SELECT zone_id, area FROM shipping_zone_areas WHERE store_id = @store_id ORDER BY zone_id, area;

-- name: ListCityRates :many
SELECT zone_id, city_key, city_name, rate_minor FROM shipping_city_rates
WHERE store_id = @store_id ORDER BY zone_id, city_name;

-- name: NextZonePosition :one
SELECT coalesce(max(position) + 1, 0)::int AS next_position FROM shipping_zones WHERE store_id = @store_id;

-- name: LockZones :exec
-- Serializes a store's zone writes (the zone count limit, positions,
-- which zone holds a governorate).
SELECT pg_advisory_xact_lock(hashtextextended('shipping:' || (@store_id::uuid)::text, 0));

-- name: InsertZone :one
INSERT INTO shipping_zones (id, store_id, name, rate_minor, free_over_minor,
                            cod_fixed_minor, cod_bp, cod_min_minor, cod_max_minor, position)
VALUES (@id, @store_id, @name, @rate_minor, sqlc.narg(free_over_minor),
        sqlc.narg(cod_fixed_minor), sqlc.narg(cod_bp), sqlc.narg(cod_min_minor), sqlc.narg(cod_max_minor), @position)
RETURNING *;

-- name: UpdateZone :one
-- 0 rows when the zone is gone or the version read is stale.
UPDATE shipping_zones
SET name = @name, rate_minor = @rate_minor, free_over_minor = sqlc.narg(free_over_minor),
    cod_fixed_minor = sqlc.narg(cod_fixed_minor), cod_bp = sqlc.narg(cod_bp),
    cod_min_minor = sqlc.narg(cod_min_minor), cod_max_minor = sqlc.narg(cod_max_minor),
    version = version + 1, updated_at = now()
WHERE store_id = @store_id AND id = @id AND version = @version
RETURNING *;

-- name: ZoneExists :one
SELECT EXISTS (SELECT 1 FROM shipping_zones WHERE store_id = @store_id AND id = @id) AS found;

-- name: DeleteZone :execrows
DELETE FROM shipping_zones WHERE store_id = @store_id AND id = @id;

-- name: DeleteZoneAreas :exec
DELETE FROM shipping_zone_areas WHERE store_id = @store_id AND zone_id = @zone_id;

-- name: InsertZoneAreas :exec
INSERT INTO shipping_zone_areas (store_id, zone_id, area)
SELECT @store_id, @zone_id, unnest(@areas::text[]);

-- name: DeleteCityRates :exec
DELETE FROM shipping_city_rates WHERE store_id = @store_id AND zone_id = @zone_id;

-- name: InsertCityRates :exec
INSERT INTO shipping_city_rates (store_id, zone_id, city_key, city_name, rate_minor)
SELECT @store_id, @zone_id, unnest(@keys::text[]), unnest(@names::text[]), unnest(@rates::bigint[]);

