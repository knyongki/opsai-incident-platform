-- name: CreateIncident :one
INSERT INTO incidents (id, device_id, title, description, alarm_code, severity, raw_logs, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: CreateAnalysisJob :one
INSERT INTO analysis_jobs (id, incident_id) VALUES ($1, $2) RETURNING *;

-- name: ListIncidents :many
SELECT i.id, i.device_id, i.title, i.alarm_code, i.severity, i.status,
       i.occurred_at, i.created_at, i.updated_at, j.status AS job_status
FROM incidents i
LEFT JOIN analysis_jobs j ON j.incident_id = i.id
WHERE (sqlc.narg(device_id)::uuid IS NULL OR i.device_id = sqlc.narg(device_id))
  AND (sqlc.narg(status)::text IS NULL OR i.status = sqlc.narg(status))
  AND (sqlc.narg(severity)::text IS NULL OR i.severity = sqlc.narg(severity))
  AND (sqlc.narg(alarm_code)::text IS NULL OR i.alarm_code = sqlc.narg(alarm_code))
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (i.created_at, i.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: GetIncident :one
SELECT i.*, j.status AS job_status
FROM incidents i
LEFT JOIN analysis_jobs j ON j.incident_id = i.id
WHERE i.id = $1;
