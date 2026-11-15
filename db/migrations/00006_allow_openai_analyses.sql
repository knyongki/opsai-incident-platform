-- +goose Up
-- OpenAI joins the real analyzers. Every non-fake source must record its model and
-- prompt version; fake rows record neither. Existing rows already satisfy this.
ALTER TABLE incident_analyses
    DROP CONSTRAINT incident_analyses_provenance_check,
    DROP CONSTRAINT incident_analyses_source_check,
    ADD CONSTRAINT incident_analyses_source_check CHECK (source IN ('fake', 'claude', 'openai')),
    ADD CONSTRAINT incident_analyses_provenance_check CHECK (
        (source = 'fake' AND model_name IS NULL AND prompt_version IS NULL)
        OR
        (source <> 'fake' AND btrim(coalesce(model_name, '')) <> ''
            AND btrim(coalesce(prompt_version, '')) <> '')
    );

-- +goose Down
-- Fails while openai rows exist, so provenance is never silently discarded.
ALTER TABLE incident_analyses
    DROP CONSTRAINT incident_analyses_provenance_check,
    DROP CONSTRAINT incident_analyses_source_check,
    ADD CONSTRAINT incident_analyses_source_check CHECK (source IN ('fake', 'claude')),
    ADD CONSTRAINT incident_analyses_provenance_check CHECK (
        (source = 'fake' AND model_name IS NULL AND prompt_version IS NULL)
        OR
        (source = 'claude' AND btrim(coalesce(model_name, '')) <> ''
            AND btrim(coalesce(prompt_version, '')) <> '')
    );
