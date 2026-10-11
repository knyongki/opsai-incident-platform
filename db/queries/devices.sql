-- name: CreateDevice :one
INSERT INTO devices (id, name, type, location, status, metadata)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: ListDevices :many
SELECT * FROM devices ORDER BY created_at, id;

-- name: GetDevice :one
SELECT * FROM devices WHERE id = $1;

-- name: UpdateDevice :one
UPDATE devices SET
name = CASE WHEN sqlc.arg(set_name)::boolean THEN sqlc.arg(name)::text ELSE name END,
type = CASE WHEN sqlc.arg(set_type)::boolean THEN sqlc.arg(type)::text ELSE type END,
location = CASE WHEN sqlc.arg(set_location)::boolean THEN sqlc.narg(location)::text ELSE location END,
status = CASE WHEN sqlc.arg(set_status)::boolean THEN sqlc.arg(status)::text ELSE status END,
metadata = CASE WHEN sqlc.arg(set_metadata)::boolean THEN sqlc.arg(metadata)::jsonb ELSE metadata END,
updated_at = clock_timestamp()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteDevice :execrows
DELETE FROM devices WHERE id = $1;
