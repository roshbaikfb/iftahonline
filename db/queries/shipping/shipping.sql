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
                            cod_fixed_minor, cod_bp, cod_min_minor, cod_max_minor,
                            eta_min_days, eta_max_days, position)
VALUES (@id, @store_id, @name, @rate_minor, sqlc.narg(free_over_minor),
        sqlc.narg(cod_fixed_minor), sqlc.narg(cod_bp), sqlc.narg(cod_min_minor), sqlc.narg(cod_max_minor),
        sqlc.narg(eta_min_days), sqlc.narg(eta_max_days), @position)
RETURNING *;

-- name: UpdateZone :one
-- 0 rows when the zone is gone or the version read is stale.
UPDATE shipping_zones
SET name = @name, rate_minor = @rate_minor, free_over_minor = sqlc.narg(free_over_minor),
    cod_fixed_minor = sqlc.narg(cod_fixed_minor), cod_bp = sqlc.narg(cod_bp),
    cod_min_minor = sqlc.narg(cod_min_minor), cod_max_minor = sqlc.narg(cod_max_minor),
    eta_min_days = sqlc.narg(eta_min_days), eta_max_days = sqlc.narg(eta_max_days),
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


-- name: InsertConnection :one
INSERT INTO carrier_connections (id, store_id, carrier, credentials, webhook_secret_hash)
VALUES (@id, @store_id, @carrier, @credentials, @webhook_secret_hash)
RETURNING *;

-- name: ListConnections :many
-- A store has a handful (one active per carrier, plus past ones).
SELECT * FROM carrier_connections WHERE store_id = @store_id ORDER BY created_at DESC LIMIT 100;

-- name: GetConnection :one
SELECT * FROM carrier_connections WHERE store_id = @store_id AND id = @id;

-- name: DisconnectConnection :one
UPDATE carrier_connections SET status = 'disconnected', disconnected_at = now(), updated_at = now()
WHERE store_id = @store_id AND id = @id AND status = 'active'
RETURNING *;

-- name: InsertShipment :one
INSERT INTO shipments (id, store_id, order_id, connection_id, carrier, currency, cod_minor)
VALUES (@id, @store_id, @order_id, @connection_id, @carrier, @currency, @cod_minor)
RETURNING *;

-- name: GetShipment :one
SELECT * FROM shipments WHERE store_id = @store_id AND id = @id;

-- name: GetShipmentForUpdate :one
-- Serializes a shipment's updates (a webhook and a poll at once).
SELECT * FROM shipments WHERE store_id = @store_id AND id = @id FOR UPDATE;

-- name: ShipmentByTracking :one
SELECT * FROM shipments WHERE store_id = @store_id AND carrier = @carrier AND tracking_number = @tracking_number;

-- name: ListShipments :many
-- Newest first, keyset on (created_at, id); optional order and status.
SELECT * FROM shipments
WHERE store_id = @store_id
  AND (created_at, id) < (@before_at::timestamptz, @before_id::uuid)
  AND (sqlc.narg(order_id)::uuid IS NULL OR order_id = sqlc.narg(order_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: SetShipmentCreated :one
UPDATE shipments SET status = 'created', tracking_number = @tracking_number, provider_id = @provider_id,
  raw_state = '', status_at = now(), updated_at = now(), version = version + 1
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: SetShipmentStatus :one
UPDATE shipments SET status = @status, raw_state = @raw_state, failure = @failure, status_at = @status_at,
  updated_at = now(), version = version + 1
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: TouchShipment :exec
-- A tracking update that changed nothing still records the raw state.
UPDATE shipments SET raw_state = @raw_state, updated_at = now() WHERE store_id = @store_id AND id = @id;

-- name: SetShipmentLabel :exec
UPDATE shipments SET label_key = @label_key, updated_at = now() WHERE store_id = @store_id AND id = @id;

-- name: RequestShipmentCancel :one
UPDATE shipments SET cancel_requested_at = coalesce(cancel_requested_at, now()), updated_at = now(), version = version + 1
WHERE store_id = @store_id AND id = @id
RETURNING *;

-- name: InsertShipmentEvent :exec
INSERT INTO shipment_events (id, store_id, shipment_id, occurred_at, status, raw_state, source, note)
VALUES (@id, @store_id, @shipment_id, @occurred_at, @status, @raw_state, @source, @note);

-- name: ListShipmentEvents :many
-- A shipment's timeline (dozens of rows at most).
SELECT * FROM shipment_events WHERE store_id = @store_id AND shipment_id = @shipment_id ORDER BY occurred_at, id LIMIT 500;

-- name: InsertSettlement :batchmany
-- One statement line, matched to its shipment by tracking number; a line
-- already imported returns no row. Sent as a batch: one round trip per
-- statement.
INSERT INTO cod_settlements (id, store_id, connection_id, carrier, tracking_number, shipment_id, currency, amount_minor, settled_at, reference, source)
VALUES (@id, @store_id, @connection_id, @carrier, @tracking_number,
  (SELECT s.id FROM shipments s WHERE s.store_id = @store_id AND s.carrier = @carrier AND s.tracking_number = @tracking_number),
  @currency, @amount_minor, @settled_at, @reference, @source)
ON CONFLICT (store_id, connection_id, tracking_number, reference) DO NOTHING
RETURNING id, shipment_id;

-- name: CODSummary :many
-- Delivered COD parcels per connection delivered in [from, to):
-- expected, settled, and the part still held, aged by delivery time as
-- of @as_of (0-7, 8-14 and 15+ days).
SELECT h.connection_id, h.carrier, h.currency,
  count(*)::int AS delivered,
  sum(h.cod_minor)::bigint AS expected_minor,
  sum(h.settled)::bigint AS settled_minor,
  sum(h.held)::bigint AS held_minor,
  sum(CASE WHEN h.age_days < 8 THEN h.held ELSE 0 END)::bigint AS held_0_7,
  sum(CASE WHEN h.age_days >= 8 AND h.age_days < 15 THEN h.held ELSE 0 END)::bigint AS held_8_14,
  sum(CASE WHEN h.age_days >= 15 THEN h.held ELSE 0 END)::bigint AS held_15_plus,
  sum(CASE WHEN h.settled > h.cod_minor THEN 1 ELSE 0 END)::int AS over_settled
FROM (
  SELECT s.connection_id, s.carrier, s.currency, s.cod_minor,
    coalesce(st.settled, 0) AS settled,
    greatest(s.cod_minor - coalesce(st.settled, 0), 0) AS held,
    extract(day FROM (sqlc.arg(as_of)::timestamptz - s.status_at)) AS age_days
  FROM shipments s
  LEFT JOIN LATERAL (SELECT sum(c.amount_minor) AS settled FROM cod_settlements c
                     WHERE c.store_id = s.store_id AND c.shipment_id = s.id) st ON true
  WHERE s.store_id = sqlc.arg(store_id) AND s.status = 'delivered' AND s.cod_minor > 0
    AND s.status_at >= sqlc.arg(from_at)::timestamptz AND s.status_at < sqlc.arg(to_at)::timestamptz
) h
GROUP BY h.connection_id, h.carrier, h.currency
ORDER BY h.carrier, h.connection_id
LIMIT 100;

-- name: UnmatchedSettlements :one
SELECT count(*)::int AS lines, coalesce(sum(amount_minor), 0)::bigint AS amount_minor
FROM cod_settlements WHERE store_id = @store_id AND shipment_id IS NULL;

-- name: HeldShipments :many
-- Delivered COD parcels not (fully) settled, oldest first: the money
-- still at the carrier, parcel by parcel.
SELECT s.id, s.order_id, s.carrier, s.tracking_number, s.currency, s.cod_minor, s.status_at,
  coalesce(st.settled, 0)::bigint AS settled_minor
FROM shipments s
LEFT JOIN LATERAL (SELECT sum(c.amount_minor) AS settled FROM cod_settlements c WHERE c.store_id = s.store_id AND c.shipment_id = s.id) st ON true
WHERE s.store_id = @store_id AND s.status = 'delivered' AND s.cod_minor > coalesce(st.settled, 0)
  AND (s.status_at, s.id) > (@after_at::timestamptz, @after_id::uuid)
ORDER BY s.status_at, s.id
LIMIT @lim;

-- name: CarrierCharges :many
-- What carriers kept of some orders' parcels, from their payouts
-- (ADR-0068): a delivered parcel's COD less what was settled for it, or
-- a returned one's negative settlements (return fees). Only parcels with
-- settlement lines are listed; one walk of shipments_store_order per
-- order.
SELECT s.order_id, s.currency,
       ((CASE WHEN s.status = 'delivered' THEN s.cod_minor ELSE 0 END) - sum(c.amount_minor))::bigint AS charge_minor
FROM unnest(@order_ids::uuid[]) AS ids(id)
JOIN shipments s ON s.store_id = @store_id AND s.order_id = ids.id AND s.status IN ('delivered', 'returned')
JOIN cod_settlements c ON c.store_id = s.store_id AND c.shipment_id = s.id
GROUP BY s.id, s.order_id, s.currency, s.status, s.cod_minor;
