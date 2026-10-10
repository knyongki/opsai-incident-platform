-- +goose Up
CREATE TABLE devices (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    location TEXT,
    status TEXT NOT NULL DEFAULT 'unknown',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE devices;
