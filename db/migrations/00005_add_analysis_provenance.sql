-- +goose Up
-- Real analyzers record which model and prompt produced a result. Legacy fake rows
-- keep NULL provenance; nothing is backfilled.
ALTER TABLE incident_analyses
    DROP CONSTRAINT incident_analyses_source_check,
    ADD CONSTRAINT incident_analyses_source_check CHECK (source IN ('fake', 'claude')),
    ADD COLUMN model_name TEXT,
    ADD COLUMN prompt_version TEXT,
    ADD CONSTRAINT incident_analyses_provenance_check CHECK (
        (source = 'fake' AND model_name IS NULL AND prompt_version IS NULL)
        OR
        (source = 'claude' AND btrim(coalesce(model_name, '')) <> ''
            AND btrim(coalesce(prompt_version, '')) <> '')
    );

-- +goose Down
-- Fails while claude rows exist, so provenance is never silently discarded.
ALTER TABLE incident_analyses
    DROP CONSTRAINT incident_analyses_provenance_check,
    DROP CONSTRAINT incident_analyses_source_check,
    DROP COLUMN model_name,
    DROP COLUMN prompt_version,
    ADD CONSTRAINT incident_analyses_source_check CHECK (source = 'fake');
