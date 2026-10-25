-- name: StartOnboarding :exec
-- Sign-up opens the wizard for its new store; running it twice is a no-op.
INSERT INTO store_onboarding (store_id) VALUES (@store_id)
ON CONFLICT (store_id) DO NOTHING;

-- name: GetOnboarding :one
SELECT * FROM store_onboarding WHERE store_id = @store_id;

-- name: ChooseTemplate :one
-- Only while the wizard is open; no row means it is closed (or was never
-- opened).
UPDATE store_onboarding
SET activity = @activity, look = @look, color = @color,
    template_status = 'preparing', template_seq = template_seq + 1, updated_at = now()
WHERE store_id = @store_id AND completed_at IS NULL
RETURNING *;

-- name: FinishTemplate :one
-- The apply job's outcome, kept only when its choice is still the
-- current one (a later choice has its own job).
UPDATE store_onboarding
SET template_status = @template_status, template_version = sqlc.narg(template_version), updated_at = now()
WHERE store_id = @store_id AND template_seq = @template_seq AND template_status = 'preparing'
RETURNING *;

-- name: CompleteOnboarding :one
UPDATE store_onboarding
SET completed_at = coalesce(completed_at, now()), updated_at = now()
WHERE store_id = @store_id
RETURNING *;

-- name: LockApply :exec
-- Serializes a store's apply jobs for the length of the caller's
-- transaction, so a later choice is applied after an earlier one, never
-- under it. Choosing does not take it: the request never waits.
SELECT pg_advisory_xact_lock(hashtextextended('onboarding:' || (@store_id::uuid)::text, 0));
