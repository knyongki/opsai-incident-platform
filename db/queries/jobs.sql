-- name: ClaimAnalysisJob :one
WITH candidate AS (
    SELECT id FROM analysis_jobs
    WHERE status = 'pending' AND available_at <= clock_timestamp()
        AND attempt_count < sqlc.arg(max_attempts)::integer
    ORDER BY available_at, id
    LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE analysis_jobs j
SET status = 'running', attempt_count = j.attempt_count + 1,
    claim_token = sqlc.arg(claim_token)::uuid, locked_at = clock_timestamp(),
    lease_expires_at = clock_timestamp() + sqlc.arg(lease_ms)::bigint * interval '1 millisecond',
    updated_at = clock_timestamp()
FROM candidate WHERE j.id = candidate.id
RETURNING j.*;

-- name: LockAnalysisJob :one
SELECT * FROM analysis_jobs WHERE id = $1 FOR UPDATE;

-- name: FinishAnalysisJob :execrows
UPDATE analysis_jobs
SET status = sqlc.arg(next_status)::text,
    available_at = clock_timestamp() + sqlc.arg(delay_ms)::bigint * interval '1 millisecond',
    last_error = sqlc.narg(error_code)::text,
    claim_token = NULL, locked_at = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg(id)::uuid AND status = 'running'
    AND claim_token = sqlc.arg(claim_token)::uuid
    AND lease_expires_at > clock_timestamp();

-- name: LockRecoverableAnalysisJobs :many
SELECT * FROM analysis_jobs
WHERE (status = 'running' AND lease_expires_at <= clock_timestamp())
   OR (status = 'pending' AND attempt_count >= sqlc.arg(max_attempts)::integer)
ORDER BY available_at, id
LIMIT 100 FOR UPDATE SKIP LOCKED;

-- name: RecoverAnalysisJob :execrows
UPDATE analysis_jobs
SET status = sqlc.arg(next_status)::text,
    available_at = clock_timestamp() + sqlc.arg(delay_ms)::bigint * interval '1 millisecond',
    last_error = sqlc.arg(error_code)::text,
    claim_token = NULL, locked_at = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg(id)::uuid
  AND ((status = 'running' AND claim_token = sqlc.narg(claim_token)::uuid
        AND lease_expires_at <= clock_timestamp())
    OR (status = 'pending' AND attempt_count >= sqlc.arg(max_attempts)::integer));
