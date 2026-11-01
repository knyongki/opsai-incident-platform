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
| 3B - Incident list/detail | Finished | Read APIs implemented and reviewed; unit, integration, Postman, and live pagination checks passed |
| 4A - Durable worker foundation | Finished | Fake worker, leases, recovery/retries, and validation passed; user accepted; committed as e2e9d1c |
| 4B - Structured analysis results | In progress | 4B.1 contract In review; persistence, worker integration, and API/Postman remain Planned |

The full incident and AI workflows remain on the blueprint. Finishing 4A does
not complete the blueprint's Milestone 4: validated, stored AI results remain later work.
Lasting contracts and commands are in the [incident read documentation](../README.md#incident-reads-milestone-3b),
[fake worker documentation](../README.md#fake-worker-milestone-4a), and
[worker setup guide](Local-Automation-Setup.md#isolated-fake-worker-milestone-4a).

## How to maintain this file

- **Planned:** a candidate task; listing it does not authorize implementation.
- **In progress:** authorized implementation is underway.
- **In review:** implementation and required checks are complete; review remains.
- **Finished:** required work and review are complete.
- Keep detailed checklists and acceptance evidence only for active/planned work.
- When a milestone is finished, remove its detailed task section and retain its
  status and short outcome in the overview. Preserve lasting contracts in README
  or the relevant guide first.
- Keep this single task file instead of separate milestone/progress documents.
- Follow [AGENTS.md](../AGENTS.md). Do not commit without an explicit request,
  push, or start later features merely because they appear here.

## Milestone 4B - In progress

**Structured analysis validation and persistence, using the fake analyzer.**

Baseline verified: 4A is committed as e2e9d1c and the working tree was clean before
this planning change. The user subsequently authorized execution of 4B.1 only.
The existing uncommitted planning changes were preserved. The proposed migration,
worker integration, and API change below still need authorization before implementation.

### Delivery in small parts

Implement one part per explicitly requested work session. Each part must build,
pass its relevant checks, and stop In review. After user acceptance, mark that
part Finished and let the user commit before starting the next part. Do not
automatically continue through all four parts.

| Part | Status | Deliverable | Depends on |
| --- | --- | --- | --- |
| 4B.1 - Result contract and validation | In review | Typed result, strict JSON decoder, semantic validation, and unit tests implemented and checked | Completed 4A |
| 4B.2 - Database persistence | Planned | Approved migration, sqlc, atomic completion repository operation, and PostgreSQL tests | Finished 4B.1 |
| 4B.3 - Worker integration | Planned | Fake structured output, input loading, validation, and atomic result completion in the running worker | Finished 4B.2 |
| 4B.4 - Detail API and Postman | Planned | Detail analysis field, local collection assertions, documentation, and end-to-end checks | Finished 4B.3 |

The sections below describe the final combined design. Intermediate parts expose
only their own deliverables; they must not claim that the entire 4B flow works.
Milestone 4B becomes Finished only after all four parts pass review.

### Outcome and scope

Today a completed job has no analysis result. In 4B, a newly processed successful
job must have exactly one validated, durable result that the API can return.

- Define a versioned analysis contract and strict validation in a small
  provider-independent `internal/analysis` package.
- Extend the fake analyzer to return deterministic structured output. Load the
  claimed incident as bounded input; no device/history retrieval is needed yet.
- Save the validated result and complete the job in one transaction, retaining
  4A claim tokens, leases, retry limits, cancellation, and safe errors.
- Add an `analysis` field to existing GET incident detail; preserve POST and list
  response shapes. No additional HTTP endpoint is needed.
- Keep WORKER_ANALYZER=fake and the separate _worker_test database restriction.
  Persist and expose trusted metadata `source: "fake"`; fake text must clearly
  state that no diagnosis occurred. Incident status remains open.
- Exclude real AI/provider SDKs, prompts, retrieval, embeddings, tool calling,
  reanalysis/retry endpoints, result history, incident lifecycle changes,
  review/resolution, MQTT, authentication, CI, and new dependencies.
- Preserve API-only startup and System-only automatic Postman execution. Cloud
  synchronization requires a separate explicit request.

This is another part of the blueprint's Milestone 4, not completion of real AI
analysis. Real providers and the analyzing/analyzed/analysis_failed transitions
need a later plan. Fake output must never be presented as engineering findings.

### Analysis contract, version 1

All result fields are required, including arrays. Reject null fields, unknown
fields, duplicate JSON keys, incorrect key casing/types, multiple JSON values,
invalid UTF-8/NUL, and oversized output. Apply a 64 KiB limit before decoding,
a maximum nesting depth of 16, and the same 64 KiB limit after serialization.
Use standard-library JSON handling with presence checks; missing confidence must
not silently become a valid zero value.

| Field | Proposed validation |
| --- | --- |
| summary | Nonblank string, up to 2,000 Unicode characters |
| primary_suspected_cause | Nonblank string, up to 1,000 characters; explicitly synthetic for fake output |
| contributing_factors | 0-10 nonblank strings, each up to 1,000 characters |
| observed_conditions | 1-10 nonblank strings, each up to 1,000 characters |
| recommended_actions | 0-10 objects with order, description, reason; no extra keys |
| safety_notes | 1-10 nonblank strings, each up to 1,000 characters |
| confidence | Finite number between 0 and 1 inclusive; fake value is 0 |
| evidence | 1-10 objects with source_id and claim; no extra keys |

Action order must be consecutive integers starting at 1 in array order.
Descriptions and reasons must be nonblank and at most 1,000 characters each.
Evidence source_id must be the current input incident UUID; claim must be nonblank
and at most 1,000 characters. Do not claim historical or document evidence exists.
Source-reference validation establishes a valid reference, not factual correctness.

A minimal fake result would contain a synthetic summary/cause, an observation
that synthetic input was received, empty contributing factors/actions, a safety
note that this is test output only, confidence 0, and evidence referencing the
input incident. It must not invent equipment-specific causes or operational advice.

The worker's consumer-owned analyzer interface should accept context plus bounded
incident input and return raw JSON plus an error. Decode and validate at the
application boundary into a typed result before persistence. Trusted metadata
(source, schema version, ID, timestamp) is assigned by the application/database,
not accepted from analyzer output. Validation failures are terminal with an
allowlisted code such as ANALYSIS_INVALID_OUTPUT; do not retry invalid JSON as a
transient network error or store its raw contents.

### Proposed schema and upgrade

After explicit migration approval, add
`00004_create_incident_analyses.sql`. Do not edit migrations 00001-00003.

| Column | Proposed definition |
| --- | --- |
| id | UUID primary key, application-generated |
| incident_id | UUID NOT NULL UNIQUE, FK to incidents(id), ON DELETE RESTRICT |
| schema_version | INTEGER NOT NULL, CHECK = 1 |
| source | TEXT NOT NULL, CHECK = 'fake' |
| result | JSONB NOT NULL, CHECK jsonb_typeof(result) = 'object' |
| created_at | TIMESTAMPTZ NOT NULL, database-generated |

The unique incident_id index supports detail lookup and at most one result per
incident, matching the existing one-job-per-incident design. Do not add job IDs,
claim tokens, duplicate confidence columns, unused model/prompt fields, review
fields, or analysis-history tables. Provider metadata and additional source types
will be reviewed when real providers are introduced.

- Existing 4A completed jobs may legitimately have no result. Preserve them and
  return analysis=null; never fabricate a result or requeue them in the migration.
- Do not infer result availability from completed status, timestamps, or version
  guesses. Without an extra job-version marker, the API cannot distinguish a
  legacy absent result from a missing result caused by external corruption.
- Use new API-created incidents to verify 4B. Existing pending jobs can be processed
  by the new worker after upgrade.
- In 4B.2, the additive table and new repository operation do not change the
  running 4A worker; it still completes jobs without results. Keep using new
  synthetic incidents for subsequent parts.
- At the 4B.3 cutover, stop all old worker processes, confirm migration 00004 is
  applied, rebuild, and start only the new worker. Mixed 4A/4B.3 workers are not
  supported: the old worker can complete without a result.
- No data deletion, historical status rewriting, private .env replacement, or Goose
  Down execution. Test upgrading populated 4A tables without modifying their rows.

### Atomic completion and failure behavior

1. Claim using the existing 4A transaction and increment attempts once.
2. Load the claimed incident with a bounded context; pass only necessary incident
   fields to the analyzer. A database read failure leaves the claim recoverable;
   do not invoke the analyzer with missing/partial input.
3. Run fake analysis and validate output outside a database transaction. Keep the
   attempt deadline through analysis/validation and check cancellation before
   finalization. Invalid output follows the guarded terminal-failure path.
4. For success, begin a short transaction and lock the job. Check running state,
   matching token, and ownership. Insert the typed, validated result for the
   incident ID obtained from the locked job, never an analyzer-supplied ID.
5. Complete the job using the existing token and current database-time expiry
   guard. If insertion or completion fails, including lease expiry while storing,
   roll back both writes. Commit only when both succeeded.
6. Clear ownership/error fields on success. A result conflict must not overwrite
   a stored analysis or silently count as success; return a sanitized invariant
   error and leave committed state unchanged.
7. If commit acknowledgement is lost, allow the existing lease/recovery policy to
   handle uncertainty. A successful commit leaves completed+result and is not
   reclaimed; rollback leaves neither completed nor a partial result.
8. Retry, terminal failure, expiry recovery, and shutdown never create results.
   Preserve 4A behavior and safe logging; never log output bodies or driver errors.

Use separate repository operations for successful completion with a validated
result and failure/retry. Add the new operation in 4B.2 without rewiring the worker;
remove the old success-without-result processing path when wiring 4B.3. Validate
at the persistence boundary as well, or use a
validated type whose invalid state cannot be accepted accidentally.

The guarantee is atomic stored state plus at most one result row. Execution may
still repeat after crashes; do not claim exactly-once analyzer execution.

### Incident detail API proposal

Extend `GET /api/v1/incidents/{id}` with exactly one additional field:

```text
data.analysis = null
or
data.analysis = {
  id: UUID,
  schema_version: 1,
  source: "fake",
  created_at: UTC timestamp,
  result: { all version-1 result fields }
}
```

- Return analysis=null for queued/running/failed jobs without a result, and legacy
  completed jobs without a result. An absent result is not an absent incident.
- New 4B success returns completed plus the stored analysis; incident stays open.
- Read incident, job status, and optional analysis in one SQL statement so a
  concurrent commit cannot produce a mixture of pre- and post-completion state.
- Retain existing status codes: 200 success, 400 invalid UUID/query, 404 nonexistent
  incident, 500 sanitized unexpected errors. Existing missing-job invariant remains.
- Reject malformed/unsupported stored analysis with a sanitized 500 rather than
  returning unvalidated JSON. A result attached to a non-completed job is an
  invariant error. No internal ownership, attempt, or failure details are exposed.
- List responses still omit analysis, description, and raw_logs. POST still returns
  the existing queued envelope.
- This is additive for tolerant clients, but existing exact-field assertions must
  change. Update handler tests and local Postman scripts together.

### Part checklists and review checkpoints

Before each part, inspect Git status, preserve existing work, and confirm the
preceding part was accepted and committed. Update AGENTS scope only for work
explicitly authorized. No new dependency, migration, or cloud sync is implied.

#### 4B.1 - Result contract and validation

- [x] Add internal/analysis types, strict JSON decoding, and semantic validation
  according to version 1 above; expose validated results for later consumers.
- [x] Add focused tests for valid results and every listed invalid-input boundary.
- [x] Document the contract and run the normal Go checks.

Done when valid synthetic JSON becomes a typed validated result and malformed or
invalid output is rejected. No database migration, analyzer-interface change,
worker wiring, HTTP response change, or Postman change in this part.

Implementation evidence (4B.1):

- `internal/analysis` exposes `Result`, `Action`, `Evidence`, `Decode`, `Validate`,
  `SchemaVersion`, `MaxJSONBytes`, and sanitized `ErrInvalid`. Mutable results must
  be revalidated at future persistence boundaries. The lasting contract is in
  [README](../README.md#analysis-result-contract-milestone-4b1).
- Tests cover valid round trips, required/exact fields, duplicates, types,
  Unicode, nulls, confidence, independent array limits, text/action/reference
  boundaries, input/serialized size, depth, safe errors, and fuzz seed invariants.
- Passed `gofmt -w cmd internal`, `go mod tidy`, `go vet ./...`,
  `go test -count=1 ./...`, and `git diff --check`.
- `docker compose --env-file .env.example config --quiet` passed with sandbox
  warnings about reading Docker's user config. No service was started.
- Focused `go test ./internal/analysis -cover` passed with 96.2% statement coverage.
  Tidy, final vet, and the coverage build needed approved access outside the sandbox after
  local cache/build failures. Fuzz seeds ran; an extended fuzz campaign was not run.
- No database/sqlc, integration, live API, or Postman checks were needed or run:
  the package is not connected to runtime behavior. No dependency changes.
- Changes reviewed for scope and public-file hygiene; no credentials or generated
  artifacts added. This was a changed-file review, not a repository/history audit.

Next checkpoint: user review/acceptance and commit of 4B.1 before starting 4B.2.

#### 4B.2 - Database persistence

- [ ] Obtain explicit approval for migration 00004 before creating/applying it.
- [ ] Add the table, result-insert query, and atomic completion operation; regenerate
  sqlc. Reuse existing job ownership checks.
- [ ] Test populated 4A upgrades, rollback, stale/expired claims, uniqueness, and
  ambiguous commit outcomes using separate PostgreSQL connections.
- [ ] Document the storage invariant and run Go plus relevant database checks.

Done when repository tests prove that one validated result and completed job
commit together, with neither partial writes nor stale-owner overwrites.
The runtime worker remains on 4A behavior; the new operation is exercised directly
by integration tests. Do not add the detail API field yet.

#### 4B.3 - Worker integration

- [ ] Extend analyzer input/output, load the claimed incident, and return deterministic
  synthetic JSON from the fake analyzer.
- [ ] Validate before storage and connect successful processing to 4B.2 atomic
  completion; remove the old success-without-result runtime path.
- [ ] Preserve retries/timeouts/recovery; add invalid-output scenarios and safe errors.
- [ ] Stop/rebuild/restart workers for the cutover. Run unit/integration tests and
  live synthetic success/failure checks; document changed fake-worker behavior.

Done when a new API-created incident is processed into one validated database
result, invalid output produces no result, and incident status stays open.
Verify persistence through repository tests or local database inspection; the
existing GET response does not expose the result until 4B.4.

#### 4B.4 - Detail API and Postman

- [ ] Implement the approved additive detail contract with one consistent SQL read.
- [ ] Test analysis=null, stored results, legacy jobs, malformed stored data,
  invalid UUID/query, missing incident/job, and sanitized failures.
- [ ] Update local detail assertions and add a separate explicit worker-result flow
  with bounded polling. Preserve System-only automation and existing IDs.
- [ ] Update README/setup documentation and run full combined validation below,
  including Postman lint, workerless checks, and live worker-result verification.

Done when Postman can retrieve the labeled fake result, existing POST/list
contracts pass unchanged, and the full 4B success criteria are satisfied.
Stop In review for user acceptance; do not synchronize cloud changes automatically.

### Success criteria and tests

| Done | Scenario | Required result |
| --- | --- | --- |
| [ ] | Valid output | All fields persist and round-trip through GET with source=fake and schema_version=1 |
| [ ] | Strict validation | Unknown/duplicate/missing/null/wrong-case keys, invalid types, trailing JSON, excessive depth/size, invalid text, confidence, actions, and source IDs are rejected |
| [ ] | Invalid analyzer output | Guarded failed job with safe error code, zero result rows, no raw output in logs/API |
| [ ] | Atomic success | Another connection observes completed+result together; forced insert or completion failure leaves neither partial result nor completed job |
| [ ] | Stale/expired owner | Old token cannot insert a result or overwrite one, including expiry between insert and final status update |
| [ ] | Duplicate completion | At most one immutable result; repeated/stale completion cannot overwrite it |
| [ ] | Ambiguous commit/crash | Commit-with-lost-ack leaves one completed result; pre-commit crash/rollback is recoverable without partial data |
| [ ] | Retries and recovery | Transient failure then success stores one result; exhaustion/permanent failure store none; 4A concurrency/cancellation checks remain valid |
| [ ] | Input loading | Analyzer gets the claimed incident only; read failure/cancellation does not invoke it with incomplete input |
| [ ] | Legacy upgrade | Existing completed-without-result stays unchanged and GET returns analysis=null; pending jobs can produce new results |
| [ ] | HTTP contract | Detail null/result, invalid UUID/query, not found, corrupt result, missing job, and storage failure tests pass; list and POST shapes unchanged |
| [ ] | Synthetic isolation | Fake mode/test database restrictions remain; incidents stay open; no real AI/network provider call |
| [ ] | Postman/live | A fresh API-created incident reaches completed with a labeled result; invalid-output scenario fails with no result; assertions pass with and without worker |
| [ ] | Normal development | go test ./... remains database-independent; existing private configuration works without edits |

Repository concurrency tests use separate connections and committed synthetic
fixtures in the dedicated test database. Inject failures after insertion and
before commit; verify rollback from another connection. Use synchronization and
bounded waits, not unbounded sleeps. Clean up only test-owned results, jobs,
incidents, and devices in foreign-key order.

### Required validation after implementation

Apply these checks to the relevant part: normal Go checks for every Go change;
sqlc regeneration when queries/schema change; database checks for 4B.2 onward;
worker live checks for 4B.3; HTTP/Postman checks for 4B.4. Run the full combined
set before final 4B review. Record evidence per part and leave later criteria
unchecked until that functionality is implemented and verified.

- `gofmt -w cmd internal`, `go mod tidy`,
  `.\bin\sqlc.exe generate -f db/sqlc.yaml`, `git diff --check`,
  `go vet ./...`, and `go test -count=1 ./...`.
- `scripts/check-local.ps1 -ChecksOnly` for formatting/vet/unit/Compose checks.
- Tagged PostgreSQL device, incident, job, migration, and new analysis tests,
  configured privately against the isolated migrated database; require PASS,
  not SKIP. Record the exact added test names and commands.
- Container health, approved Goose up/status, and live API readiness.
- Postman YAML lint, unchanged System run, workerless Incidents - Manual, and
  explicit worker-result checks. Preserve collection/request IDs.
- Live success and invalid-output checks using API-created synthetic incidents;
  verify persisted results still read correctly after restarting the API.
- Review tracked/untracked changes for secrets, generated artifacts, dependencies,
  scope, and needless comments. No commit, push, or cloud sync without authorization.

### Design review and remaining decisions

Reviewed against the current 4A implementation and blueprint using invariants and
failure scenarios: result integrity, ownership loss, rollback, compatibility,
and how clients distinguish fake output.

| Risk | Recommended decision |
| --- | --- |
| Fake output looks like real findings | Trusted source=fake, deterministic test-only wording, confidence 0, incident stays open |
| Completed status without saved output | New completion transaction requires a validated result; preserve legacy completed+null explicitly |
| Stale worker inserts before losing ownership | Lock job first; insert and final lease-checked completion in one transaction; rollback both on failure |
| JSON validation is mistaken for engineering correctness | Validate structure/references only; do not claim a correct diagnosis |
| Exact-field clients break | Explicit additive detail-contract approval and updated local assertions |
| Old workers bypass result persistence | Stop/rebuild/restart workers during upgrade; no mixed-version execution |
| Current one-job model grows into unplanned history | One immutable result per incident; defer reanalysis/history design |

Only the 4B.1 contract and validation are implemented and In review. The persistence,
worker integration, and detail API designs remain proposals. No migration or cloud
operation was performed. Keep the overall 4B milestone In progress until all parts
pass their required checks and review; mark Finished only after user acceptance.
