# SQL queries

`devices.sql` contains create, list, get, partial update, and delete queries.
The authoritative schema is `../migrations/00001_create_devices.sql`.
Run `.\bin\sqlc.exe generate -f db/sqlc.yaml` from the repository root after
installing the pinned local sqlc tool as documented in the root README.
Generated code belongs in `internal/platform/database/dbgen`.
