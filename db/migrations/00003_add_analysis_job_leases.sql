-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM analysis_jobs WHERE status = 'running' OR locked_at IS NOT NULL) THEN
        RAISE EXCEPTION 'Analysis jobs require ownership review before lease migration';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE analysis_jobs
    ADD COLUMN claim_token UUID,
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT analysis_jobs_lease_check CHECK (
        (status = 'running' AND claim_token IS NOT NULL AND locked_at IS NOT NULL
            AND lease_expires_at IS NOT NULL AND lease_expires_at > locked_at)
        OR
        (status <> 'running' AND claim_token IS NULL AND locked_at IS NULL
            AND lease_expires_at IS NULL)
    );
CREATE INDEX analysis_jobs_running_lease_idx
    ON analysis_jobs (lease_expires_at, id) WHERE status = 'running';

-- +goose Down
ALTER TABLE analysis_jobs DROP CONSTRAINT analysis_jobs_lease_check;
DROP INDEX analysis_jobs_running_lease_idx;
ALTER TABLE analysis_jobs DROP COLUMN claim_token, DROP COLUMN lease_expires_at;
