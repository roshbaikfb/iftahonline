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
