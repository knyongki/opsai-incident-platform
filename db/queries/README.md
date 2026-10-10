# SQL queries

Reserved for named sqlc queries in a later milestone. No device queries or
generated code are included in Milestone 2A. `db/sqlc.yaml` already selects
PostgreSQL, Goose migrations, and the pgx/v5 Go driver. Paths are relative to
that configuration file. Run generation only after adding the first queries;
an empty query directory is not a complete generation input.
