# OpsAI tasks

Use this file for current status and active tasks. The
[project blueprint](OpsAI-Project-Blueprint.md) keeps the general milestone roadmap;
the [README](../README.md) describes implemented behavior and operating instructions.

## Status overview

| Milestone | Status | Outcome or next step |
| --- | --- | --- |
| 1 - Go API foundation | Finished | Configuration, logging, health/readiness, graceful shutdown, and unit tests |
| 2A - PostgreSQL foundation | Finished | Compose, connection pooling, database readiness, Goose migrations, and sqlc |
| 2B - Device management | Finished | Device CRUD, validation, HTTP tests, and repository integration tests |
| 3A - Incident creation and durable jobs | Finished | Reviewed, verified, and committed as b9c4e4d |
| 3B - Incident list/detail | Finished | Read APIs implemented; unit, integration, Postman, and live pagination checks passed |

Milestone 3A is part of the blueprint's wider Milestone 3; the full incident
workflow is not finished. Later work remains on the blueprint.

## How to maintain this file

- **Planned:** a candidate task; listing it does not authorize implementation.
- **In progress:** authorized implementation is underway.
- **In review:** implementation and required checks are complete; review remains.
- **Finished:** required work and review are complete.
- Keep detailed checklists and acceptance evidence only for active milestones.
- When a milestone is finished, remove its detailed task section and retain its
  status and short outcome in the overview. Keep lasting API contracts and
  operating instructions in README or the relevant guide before removing details.
- Keep this single task file instead of separate milestone/progress documents.
- Follow [AGENTS.md](../AGENTS.md). Do not create commits without an explicit
  request, push, or start later features merely because they appear here.

## Milestone 3B - Finished

Prerequisite verified: 3A committed as b9c4e4d; working tree clean before edits.

- [x] Implement GET /api/v1/incidents and GET /api/v1/incidents/{id}.
- [x] Add newest-first cursor pagination, limit 1-100 (default 20), and exact filters.
- [x] Reject invalid, duplicate, and unknown query parameters; bind cursors to filters.
- [x] Map job statuses without exposing internal job fields; preserve nullable fields.
- [x] Add sqlc queries and regenerate code without a migration or new dependency.
- [x] Add domain, HTTP, and PostgreSQL integration tests.
- [x] Add local Postman YAML requests; keep automatic runs System-only.
- [x] Update README and validate live list/detail against API-created incidents.
- [x] Run formatting, tidy, sqlc, vet, uncached unit tests, YAML lint, integration tests,
  Compose checks, and diff review; record actual results here.

### Contract and implementation decisions

- List supports limit, cursor, device_id, status, severity, and alarm_code only.
  Exact filters combine with AND. Duplicate/unknown parameters and invalid values
  return 400. Empty alarm_code matches an empty stored string; other empty values
  are invalid. Detail rejects query parameters.
- Ordering is created_at DESC, id DESC. Fetch limit + 1; return a cursor for the
  last included item only when more rows exist. Empty items is [], never null.
- Opaque version-1 base64url cursor contains the timestamp/UUID boundary and a
  normalized-filter hash. Changing filters returns 400; changing limit is allowed.
- List omits description/raw_logs. Detail includes all incident fields and nullable
  values. Both expose only the client-facing job status (pending becomes queued).
- ListIncidents and GetIncident are regenerated sqlc queries over the existing
  schema. A left join preserves visibility of incidents with missing jobs, which
  produce a sanitized 500 invariant error. Missing incidents return 404.
- No new migrations, dependencies, or worker behavior. Cloud synchronization was
  performed afterward under a separate explicit user request.

See the [README contract](../README.md#incident-reads-milestone-3b) for field names,
examples, status codes, and repeatable test commands.

### Actual validation

| Check | Result |
| --- | --- |
| gofmt -w cmd internal | Completed; subsequent runner formatting check passed |
| go mod tidy | Passed; go.mod and go.sum unchanged |
| bin/sqlc.exe generate -f db/sqlc.yaml | Passed; generated incident queries updated |
| go vet ./... | Passed |
| go test -count=1 ./... | Passed, database-independent |
| Postman YAML lint | 19 requests; no errors or warnings |
| Full check-local.ps1 | Passed; Compose configuration, PostgreSQL healthy, Goose up/status, and temporary API readiness |
| System folder on port 18080 | 2 requests, 7 assertions, zero failures |
| Repository integration tests | Device, incident creation, and incident reads all PASS; no skips |
| Incident read integration cases | Ordering, UUID ties, cursor traversal, all filters, detail/nulls, missing incident and missing-job invariant passed |
| Incidents - Manual on fresh API port 18081 | 7 requests, 18 assertions, zero failures |
| Additional live HTTP checks | Three API-created incidents traversed with limit=1 and combined filters; no duplicate/missing rows; detail/logs/nulls passed |
| Diff/documentation review | Whitespace and 24 local links/anchors passed; no migration/module changes or unrelated feature additions |
| Public-file checks | Changed/new file credential-pattern check passed; .env and bin artifacts ignored and untracked; not a history audit |

Initial sandbox build-cache access was denied; the approved run outside the
sandbox passed. Live fixtures left 2 synthetic devices, 4 incidents, and 4 jobs.
Integration fixtures were cleaned up/rolled back. Temporary APIs were stopped;
PostgreSQL and any previously running development API were left running.

### Remaining limitations and review

- Cursors are not signed authorization tokens or cross-request snapshots.
  Newer insertions require restarting pagination; updates can change membership.
- No load testing or new pagination index; query performance on large datasets
  should be measured before proposing an index migration.
- Postman 3B synchronization is complete under the follow-up user request: seven
  incident requests fetched back and verified for order, methods, URLs, bodies,
  and scripts; incident_id variable present; System/device folders unchanged.
  No new live run was performed during synchronization; the prior local run passed
  18 assertions. Future synchronization still requires an explicit request.
- [ ] User reviews Milestone 3B. Keep In review until accepted.

Scope excludes workers, AI, retrieval, analysis results, retries, review/resolution,
MQTT, authentication, automatic cloud synchronization, commits, and pushes.
Stopped In review after implementation and required validation. No commit or push.
