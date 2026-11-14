# Worker and analysis

Creating an incident stores an analysis job. A separate worker process claims
jobs, runs an analyzer, validates its output, and stores the result. Today the
only analyzer is a deterministic **fake** used for development and tests; it
performs no diagnosis. Real AI providers are planned in the
[blueprint](OpsAI-Project-Blueprint.md).

To run the worker locally, follow the
[isolated worker setup](Local-Automation-Setup.md#isolated-fake-worker).

## Safety boundaries

- The worker runs only when `WORKER_ANALYZER=fake` is set explicitly; there is no
  enabling default.
- `WORKER_DATABASE_TARGET` chooses exactly one database, and both targets must be
  on localhost:
  - `isolated` (the default) uses `WORKER_DATABASE_URL`, whose name must end in
    `_worker_test`. It never falls back to `DATABASE_URL`.
  - `main` uses `DATABASE_URL`, the API's database, and never falls back to
    `WORKER_DATABASE_URL`. Startup logs a warning. Fake results written there are
    permanent: each incident holds one result and reanalysis is not modeled.

  URLs allow only `sslmode`, `connect_timeout`, and `application_name` query
  parameters, so connection overrides cannot bypass these checks.
- Fake results are labeled `source: "fake"`, state that no diagnosis was performed,
  use confidence 0, and reference only the incident itself as evidence.
- The worker moves the incident status with the job in one transaction:
  `analyzing` on claim (and while a retry waits), `analyzed` with a stored result,
  `analysis_failed` on a terminal failure. A `resolved` incident is never changed.
- Result bodies, raw analyzer output, and database errors are never logged. Only
  safe error codes are stored.

## Processing a job

1. **Recover:** jobs whose lease expired are rescheduled or failed (up to 100 per pass).
2. **Claim:** a short transaction picks the next available job with `SKIP LOCKED`,
   increments its attempt count, and records a fresh claim token and lease expiry.
3. **Load input:** only the claimed incident's fields, checked against the claim
   token. A storage failure here releases the job for retry with
   `ANALYSIS_INPUT_UNAVAILABLE`; the analyzer is never called with partial input.
4. **Analyze:** outside any transaction, bounded by the attempt timeout. A late
   success after the deadline counts as a timeout.
5. **Validate:** the analyzer returns raw JSON, decoded with the strict
   [analysis contract](#analysis-result-contract).
6. **Finish:** a valid result is stored in the same transaction that completes the
   job. Failures are recorded with a safe code and either retried or made terminal.

Every final write requires the current claim token and an unexpired lease, so an
expired or stale worker cannot overwrite a newer owner. All transition times come
from PostgreSQL. Ctrl+C cancels active work and stops claiming; unfinished claims
are left for lease recovery.

| Outcome | Job status | Code |
| --- | --- | --- |
| Valid result | `completed`, result stored | — |
| Transient failure or attempt timeout | `pending` with capped backoff, then `failed` when attempts run out | `ANALYSIS_TRANSIENT`, `ANALYSIS_TIMEOUT` |
| Permanent failure | `failed` | `ANALYSIS_PERMANENT` |
| Invalid output, including a result rejected while storing | `failed`, nothing stored | `ANALYSIS_INVALID_OUTPUT` |
| Input could not be loaded | `pending` with backoff | `ANALYSIS_INPUT_UNAVAILABLE` |
| Lease expired | rescheduled or `failed` by recovery | `LEASE_EXPIRED`, `ATTEMPTS_EXHAUSTED` |

Delivery is at least once: a crash can repeat execution, and a claim consumes an
attempt even if the process dies before analysis. Retries have a fixed cap, so
success is not guaranteed. There is no heartbeat and no exactly-once guarantee.
Storage failures use bounded backoff; an uncertain completion is recovered rather
than immediately re-executed. Run all workers on the same queue with the same
policy, and never mix worker versions that complete jobs differently.

## Configuration

| Setting | Default | Meaning |
| --- | --- | --- |
| `WORKER_ANALYZER` | Empty; must explicitly be `fake` | Enables only the fake implementation |
| `WORKER_DATABASE_TARGET` | `isolated` | `isolated` uses `WORKER_DATABASE_URL`; `main` uses `DATABASE_URL` |
| `WORKER_DATABASE_URL` | Required for the isolated target | Separate localhost database ending in `_worker_test` |
| `WORKER_FAKE_OUTCOME` | `success` | `success`, `transient`, `permanent`, `transient-once`, or `invalid-output` |
| `WORKER_FAKE_DELAY` | `100ms` | Simulated work; cancellation-aware |
| `JOB_POLL_INTERVAL` | `1s` | Idle polling delay |
| `JOB_ATTEMPT_TIMEOUT` | `30s` | Analyzer deadline |
| `JOB_LEASE` | `60s` | Fixed ownership duration |
| `JOB_DATABASE_TIMEOUT` | `5s` | Bounded storage operation |
| `JOB_RETRY_BASE` / `JOB_RETRY_CAP` | `2s` / `60s` | Capped exponential retry delay |
| `JOB_MAX_ATTEMPTS` | `3` | Claim limit, 1-100 |
| `WORKER_SHUTDOWN_TIMEOUT` | `10s` | Maximum wait for cancellation |

Durations must be between 1ms and 24h (fake delay may also be zero). The lease
must exceed the attempt timeout plus three database timeouts (claim, input load,
and completion), and the retry cap must be at least the retry base. These
WORKER_/JOB_ keys are optional for the API.

## Analysis result contract

`internal/analysis` defines version 1 with standard-library-only types.
`Decode(data []byte, incidentID string) (Result, error)` accepts one complete JSON
result; `Validate(result Result, incidentID string) error` checks typed values.
Both return the sanitized `ErrInvalid` without including the input. A `Result` is
mutable, so every persistence or read boundary validates it again.

All fields are required, non-null, and use exactly these names. Empty arrays are
allowed only where the minimum is zero; nil slices are invalid.

| Field | Contract |
| --- | --- |
| `summary` | Nonblank string, at most 2,000 Unicode code points |
| `primary_suspected_cause` | Nonblank string, at most 1,000 code points |
| `contributing_factors` | 0–10 nonblank strings, at most 1,000 code points each |
| `observed_conditions` | 1–10 strings with the same text limits |
| `recommended_actions` | 0–10 objects containing exactly `order`, `description`, `reason` |
| `safety_notes` | 1–10 strings with the same text limits |
| `confidence` | Required finite number from 0 through 1, inclusive |
| `evidence` | 1–10 objects containing exactly `source_id`, `claim` |

Action order is consecutive integers starting at 1 in array order. Descriptions,
reasons, and evidence claims are nonblank and at most 1,000 code points. Each
evidence source must be a hyphenated UUID matching the input incident
(case-insensitive). Text is preserved without trimming or normalization.
Reference validation establishes neither factual accuracy nor a correct diagnosis.

The decoder rejects unknown, duplicate (including escaped equivalents), missing,
wrong-case, and incorrectly typed fields, null values, trailing JSON or content,
invalid UTF-8, unpaired Unicode surrogate escapes, and NUL characters. Input is
limited to 64 KiB before decoding, nesting to 16 containers, and the result to
64 KiB after standard Go JSON serialization (including HTML escaping).
`SchemaVersion` is 1. Trusted metadata (source, version, IDs, timestamps) is not
part of the result JSON; the application and database assign it. The contract
permits the full confidence range; the fake analyzer always uses 0.

## Storage

Migration `00004_create_incident_analyses.sql` stores at most one immutable result
per incident: an application-generated UUID, schema version 1, trusted source
`fake`, a JSONB result, and a database-generated timestamp. Completed jobs from
before this table existed keep no result and are never given a fabricated one.

`JobRepository.FinishWithResult` locks the job, checks the claim token and lease,
validates the result against the incident ID read from the locked row, inserts the
result, and completes the job in one transaction. A failed insert, completion, or
commit leaves no partial state. A lost commit acknowledgement can still mean the
transaction committed, so callers rely on the existing lease recovery. Successful
jobs complete only through this operation.

Stored results are returned by incident detail as the
[`analysis` field](API-Reference.md#analysis), after being decoded again.
