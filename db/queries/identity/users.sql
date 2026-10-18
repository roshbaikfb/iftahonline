-- users and user_sessions are platform-wide (no store_id, no tenant RLS);
-- only the identity module queries them.

-- name: InsertUser :one
INSERT INTO users (id, email, name, password_hash)
VALUES (@id, @email, @name, @password_hash)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = @email;

-- name: GetUser :one
SELECT * FROM users WHERE id = @id;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = @phone;

-- name: SetVerifiedPhone :exec
UPDATE users SET phone = @phone, phone_verified_at = now(), updated_at = now() WHERE id = @id;

-- name: SetPendingTOTP :execrows
-- Starts (or restarts) enrolment; refused while 2FA is enabled.
UPDATE users SET totp_secret = @totp_secret, updated_at = now()
WHERE id = @id AND totp_enabled_at IS NULL;

-- name: EnableTOTP :execrows
UPDATE users SET totp_enabled_at = now(), totp_last_step = @step, updated_at = now()
WHERE id = @id AND totp_secret IS NOT NULL AND totp_enabled_at IS NULL;

-- name: UseTOTPStep :execrows
-- Atomic replay guard: a step is accepted once, and only moving forward.
UPDATE users SET totp_last_step = @step WHERE id = @id AND totp_last_step < @step;

-- name: DisableTOTP :exec
UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = 0, updated_at = now() WHERE id = @id;

-- name: InsertRecoveryCodes :copyfrom
INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (@user_id, @code_hash);

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id = @user_id;

-- name: UseRecoveryCode :execrows
UPDATE user_recovery_codes SET used_at = now()
WHERE user_id = @user_id AND code_hash = @code_hash AND used_at IS NULL;

-- name: UpsertDevice :one
-- Records a sign-in device; inserted is true the first time it is seen.
INSERT INTO user_devices (user_id, device_hash, user_agent, ip_prefix)
VALUES (@user_id, @device_hash, @user_agent, @ip_prefix)
ON CONFLICT (user_id, device_hash) DO UPDATE
  SET last_seen_at = now(), user_agent = excluded.user_agent, ip_prefix = excluded.ip_prefix
RETURNING (xmax = 0)::boolean AS inserted;

-- name: CountDevices :one
SELECT count(*) FROM user_devices WHERE user_id = @user_id;
