-- The founding-merchants program (S4b-2).

-- name: FoundingOpen :one
-- The program's discount for a merchant subscribing now, or 0.
SELECT app.founding_open(@owner_id)::int AS discount_bps;

-- name: FoundingJoin :one
-- The store's seat once its first subscription invoice is paid, or 0.
SELECT app.founding_join(@store_id, @owner_id, @currency, @plan_code, @cycle, @locked_minor)::int AS seat;

-- name: FoundingMember :one
SELECT store_id, owner_id, seat, discount_bps, currency, plan_code, cycle, locked_minor, joined_at, lost_at, lost_reason
FROM founding_members WHERE store_id = @store_id;

-- name: FoundingRelock :exec
-- A member's renewal of another plan or cycle locks that price.
UPDATE founding_members SET currency = @currency, plan_code = @plan_code, cycle = @cycle, locked_minor = @locked_minor, updated_at = now()
WHERE store_id = @store_id AND lost_at IS NULL;

-- name: FoundingSweep :one
SELECT app.founding_sweep(@now, @unpaid_before)::int AS lost;

-- name: FoundingProgram :one
SELECT enabled, seats, discount_bps FROM founding_program WHERE id = 1;

-- name: SaveFoundingProgram :exec
UPDATE founding_program SET enabled = @enabled, seats = @seats, discount_bps = @discount_bps, updated_at = now() WHERE id = 1;

-- name: FoundingSeats :one
SELECT enabled::boolean AS enabled, seats::int AS seats, taken::int AS taken, discount_bps::int AS discount_bps FROM app.founding_seats();

-- name: FoundingList :many
SELECT store_id::uuid AS store_id, seat::int AS seat, discount_bps::int AS discount_bps, currency::text AS currency, plan_code::text AS plan_code,
       cycle::text AS cycle, locked_minor::bigint AS locked_minor, joined_at::timestamptz AS joined_at,
       -- Go's zero time (IsZero) for a member still in.
       coalesce(lost_at, '0001-01-01 00:00:00+00')::timestamptz AS lost_at, coalesce(lost_reason, '')::text AS lost_reason
FROM app.founding_list();
