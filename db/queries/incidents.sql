-- name: CreateIncident :one
INSERT INTO incidents (id, device_id, title, description, alarm_code, severity, raw_logs, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: CreateAnalysisJob :one
INSERT INTO analysis_jobs (id, incident_id) VALUES ($1, $2) RETURNING *;
