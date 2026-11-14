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
| 4B - Structured analysis results | In progress | 4B.1-4B.3 Finished; 4B.4 Planned |

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

Baseline verified: 4A is committed as e2e9d1c and 4B.1 is committed as 2e2bd96.
The user authorized the parts in separate work sessions. Migration 00004 was approved for 4B.2. Worker integration is authorized for 4B.3; API changes remain separately scoped to 4B.4.

### Delivery in small parts

Implement one part per explicitly requested work session. Each part must build,
pass its relevant checks, and stop In review. After user acceptance, mark that
part Finished and let the user commit before starting the next part. Do not
automatically continue through all four parts.

| Part | Status | Deliverable | Depends on |
| --- | --- | --- | --- |
| 4B.1 - Result contract and validation | Finished | Committed typed result contract, strict decoder, semantic validation, tests, and README documentation | Completed 4A |
| 4B.2 - Database persistence | Finished | Migration 00004, sqlc insert, atomic result/job completion, and PostgreSQL integration tests; committed as 66f69a5 | Finished 4B.1 |
| 4B.3 - Worker integration | Finished | Fake structured output, input loading, validation, and atomic result completion in the running worker; accepted 2026-11-14 | Finished 4B.2 |
| 4B.4 - Detail API and Postman | Planned | Detail analysis field, local collection assertions, documentation, and end-to-end checks | Finished 4B.3 |

The sections below describe the final combined design. Intermediate parts expose
only their own deliverables; they must not claim that the entire 4B flow works.
Milestone 4B becomes Finished only after all four parts pass review.

### Outcome and scope

4B.3 connects the fake worker to 4B.2 atomic persistence. Successful new jobs now have a validated result; the API remains unchanged until 4B.4.

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
explicitly authorized. No dependency, later migration, or cloud sync is authorized
by the plan alone.

#### 4B.2 - Database persistence

- [x] Obtain explicit approval for migration 00004 before creating/applying it.
- [x] Add the table, result-insert query, and atomic completion operation; regenerate
  sqlc. Reuse existing job ownership checks.
- [x] Test populated 4A upgrades, rollback, stale/expired claims, uniqueness, and
  ambiguous commit outcomes using separate PostgreSQL connections.
- [x] Document the storage invariant and run Go checks.

Implementation evidence so far: migration 00004 is additive; sqlc generated the
insert query; `FinishWithResult` revalidates against the locked database incident
ID, inserts one `source=fake`, version-1 JSONB result, and completes the unexpired
owned job in the same transaction. Tagged tests cover existing-job upgrades,
constraint checks, independent-connection visibility, rollback on insertion and
completion failures, stale/expired ownership, duplicate protection, and ambiguous
commit outcomes. The worker and API are unchanged.

Validation: the user ran `scripts/start-worker.ps1 -MigrateOnly` and reported
Goose up/status success. `TestFinishWithResultIntegration` and
`TestIncidentAnalysisMigrationIntegration` passed, followed by the full tagged
database suite with both test URL variables set to the isolated worker database;
device, incident, read, job, lease-migration, and analysis tests all passed.
`gofmt -w cmd internal`, `go mod tidy`, `go vet ./...`,
`go test -count=1 ./...`, sqlc generation, Goose migration validation,
`docker compose --env-file .env.example config --quiet`, and `git diff --check`
passed. The Compose check printed sandbox warnings about reading Docker's user
config. No credentials were displayed or changed.

Done when repository tests prove that one validated result and completed job
commit together, with neither partial writes nor stale-owner overwrites.
4B.2 added atomic persistence without rewiring the worker; 4B.3 now connects those operations. Do not add the detail API field until 4B.4.

#### 4B.3 - Worker integration (Finished)

Connect the fake worker to the result contract and the `FinishWithResult`
repository operation from 4B.2. This part is implemented,
verified, and accepted by the user on 2026-11-14.

**Processing flow**

1. After claiming a job, load only that job's incident fields using its ID and
   ownership token. Bound the query with the existing database timeout and use
   existing incident field limits. Do not load device history, similar incidents,
   documents, or any retrieval context.
2. Pass a small incident input to the analyzer. Change the fake analyzer to return
   raw JSON so the worker exercises the same strict decoder used for future output.
3. Return deterministic synthetic text only: clearly say no diagnosis was made,
   use confidence 0, and reference the claimed incident ID as the sole evidence.
   Do not invent equipment-specific causes or operational advice.
4. Decode and validate the analyzer JSON with `internal/analysis`. On success,
   call `FinishWithResult`; remove the worker's success path that completes jobs
   without a result.
5. Keep transient analyzer and input-loading errors on the existing bounded retry
   path. Treat invalid JSON or semantic validation failure as terminal, using the
   safe code `ANALYSIS_INVALID_OUTPUT`. Keep permanent errors terminal. Never log
   or persist raw invalid output.
6. Preserve the 4A claim token, lease expiry guard, recovery, timeouts, bounded
   shutdown, and cancellation behavior. If input loading fails or is cancelled,
   do not invoke the analyzer with partial data. Failure, retry, expiry, and
   cancellation paths must not create an analysis row.

**Implementation checklist**

- [x] Add the minimal worker analysis input type and update the consumer-owned
  analyzer/repository interfaces for input loading and raw JSON output.
- [x] Add a bounded, claim-checked incident-input query/operation. Do not add a
  migration or third-party dependency.
- [x] Update the fake analyzer with deterministic valid output and an explicit
  invalid-output test mode. Keep existing success, transient, transient-once,
  and permanent scenarios working.
- [x] Validate output before persistence. Route success to `FinishWithResult`;
  route invalid output to terminal failure with `ANALYSIS_INVALID_OUTPUT`.
- [x] Add worker unit tests for input forwarding, valid and invalid output,
  analyzer/read failures, retry classification, cancellation, and safe errors.
- [x] Add PostgreSQL worker integration tests for valid success, invalid output,
  transient-then-success, retry exhaustion, and absence of partial results.
- [x] Document the new fake-worker flow and manual checks. Preserve System-only
  Postman automation and leave API responses and the collection unchanged.
- [x] Document that 4A worker processes must stop before using 4B.3; mixed worker
  versions are unsupported because 4A can complete a job without a result.

**Acceptance checks**

Implementation evidence: the worker loads only the current incident through a
claim-token and lease-checked query, validates raw fake JSON, and stores success
through `FinishWithResult`. Invalid output is terminal with
`ANALYSIS_INVALID_OUTPUT`; retries and cancellation keep the 4A ownership rules.
PostgreSQL tests exercise current-incident input/null fields, stale-token rejection,
success with one `source=fake` result and open incident, invalid output with no result,
and transient retry followed by one result. README and setup guidance describe the
new behavior and invalid-output mode. HTTP and Postman files were not changed.

Validation passed: `gofmt -w cmd internal`, `go mod tidy`, `sqlc generate`,
`go vet ./...`, `go test -count=1 ./...`, `git diff --check`, Compose validation,
and the full tagged PostgreSQL database suite. `scripts/check-local.ps1` passed
container health, migrations, API readiness, and System Postman assertions.

Review fixes (2026-11-14): the lease must now exceed attempt timeout plus three
database timeouts (claim, input load, completion); an input-load storage failure
releases the claim as retryable `ANALYSIS_INPUT_UNAVAILABLE`; a result rejected
inside the completion transaction is recorded as `ANALYSIS_INVALID_OUTPUT`. Unit
tests cover each fix; Go checks, `check-local.ps1`, and the tagged suite passed after them.

Live check (2026-11-14): with the separate worker process and a second API on
127.0.0.1:18082 using the `_worker_test` database, Postman Incidents - Manual
created incidents. In `success` mode GET detail showed `analysis_status=completed`;
in `invalid-output` mode, `failed`. After restarting that API, the completed
incident still read as `completed`; incidents stayed open and response shapes were
unchanged. Job/result rows and the invalid-output log error code were verified by
the tagged integration tests, not inspected directly during the live run.

- A new synthetic incident processed by the worker has a `completed` job and
  exactly one version-1 result with `source=fake`, committed atomically; its
  incident status remains `open`.
- Invalid output ends in `failed` with the safe allowlisted error code, no result,
  and no raw output in logs or API responses.
- A transient failure followed by success stores exactly one result. Permanent
  failure and exhausted retries store none. Existing lease/recovery/cancellation
  checks continue to pass.
- Input read failure or cancellation never invokes the analyzer with incomplete
  input. Stale or expired ownership cannot write a result.
- Normal `go test ./...` remains PostgreSQL-independent. All tagged repository
  tests pass against the isolated migrated `_worker_test` database.
- Manual verification creates an incident through the API configured to use the
  worker test database, runs the fake worker, and checks job/result rows. Repeat
  with invalid-output mode and verify failure with zero result rows. GET detail
  does not show the result until 4B.4.

Stop 4B.3 In review after checks and documentation pass. User acceptance and a
separate commit are required before planning or starting 4B.4.

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

The 4B.1 contract, 4B.2 persistence, and 4B.3 worker integration are Finished;
the 4B.4 detail API remains planned. Keep overall 4B In progress until all
parts pass their required checks and review; mark Finished only after user acceptance.
