# Worker and analysis

Creating an incident stores an analysis job. A separate worker process claims
jobs, runs an analyzer, validates its output, and stores the result. There are three
analyzers, and one must be chosen explicitly:

- **`fake`**: deterministic output for development and tests; it performs no
  diagnosis.
- **`claude`**: calls the Claude Messages API with a versioned prompt and
  structured output (see [Claude analyzer](#claude-analyzer)). Each analysis sends
  incident and device details to Anthropic and is billed.
- **`openai`**: calls the OpenAI Responses API with the same prompt and output
  contract (see [OpenAI analyzer](#openai-analyzer)). Each analysis sends incident
  and device details to OpenAI and is billed.

All real analyzers share one prompt package and one set of outcomes, so their
results are stored and returned the same way, labeled with `source`,
`model_name`, and `prompt_version`.

To run the worker locally, follow the
[main-database worker](Local-Automation-Setup.md#fake-worker-on-the-main-database),
[isolated worker](Local-Automation-Setup.md#isolated-fake-worker), or
[Claude worker](Local-Automation-Setup.md#claude-worker), or
[OpenAI worker](Local-Automation-Setup.md#openai-worker) setup.

## Safety boundaries

- The worker runs only when `WORKER_ANALYZER` is set explicitly to `fake`,
  `claude`, or `openai`; there is no enabling default.
- `WORKER_DATABASE_TARGET` chooses exactly one database, and both targets must be
  on localhost:
  - `isolated` (the default) uses `WORKER_DATABASE_URL`, whose name must end in
    `_worker_test`. It never falls back to `DATABASE_URL`.
  - `main` uses `DATABASE_URL`, the API's database, and never falls back to
    `WORKER_DATABASE_URL`. Startup logs a warning. Results written there are
    permanent: each incident holds one result and reanalysis is not modeled.

  URLs allow only `sslmode`, `connect_timeout`, and `application_name` query
  parameters, so connection overrides cannot bypass these checks.
- Fake results are labeled `source: "fake"`, state that no diagnosis was performed,
  use confidence 0, and reference only the incident itself as evidence.
- Claude and OpenAI results are labeled `source: "claude"` or `source: "openai"`
  with the answering model and the prompt version. They are decision support: a
  validated structure does not make a diagnosis correct, and the prompt forbids
  claiming a confirmed root cause.
- The worker moves the incident status with the job in one transaction:
  `analyzing` on claim (and while a retry waits), `analyzed` with a stored result,
  `analysis_failed` on a terminal failure. A `resolved` incident is never changed.
- Result bodies, raw analyzer output, prompts, incident text, provider error
  messages, API keys, and database errors are never logged. Only safe error codes
  are stored; logs carry IDs, durations, model, and token counts.

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
| Model refused the request, even after the fallback | `failed` | `ANALYSIS_REFUSED` |
| Provider rejected the key, access, billing, model, or effort | `pending` with the attempt given back, incident back to `open`; the worker stops | `ANALYSIS_PROVIDER_CONFIG` |
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
| `WORKER_ANALYZER` | Empty; must explicitly be `fake`, `claude`, or `openai` | Chooses the analyzer |
| `WORKER_DATABASE_TARGET` | `isolated` | `isolated` uses `WORKER_DATABASE_URL`; `main` uses `DATABASE_URL` |
| `WORKER_DATABASE_URL` | Required for the isolated target | Separate localhost database ending in `_worker_test` |
| `WORKER_FAKE_OUTCOME` | `success` | `success`, `transient`, `permanent`, `transient-once`, or `invalid-output` |
| `WORKER_FAKE_DELAY` | `100ms` | Simulated work; cancellation-aware |
| `WORKER_AI_API_KEY` | Required for `claude` and `openai` | The chosen provider's API key; never printed or logged |
| `WORKER_AI_MODEL` | `claude-opus-5-5` for `claude`; **required** for `openai` | Model ID |
| `WORKER_AI_EFFORT` | `high` | `low`, `medium`, `high`, `xhigh`, or `max`; the model must support it |
| `JOB_POLL_INTERVAL` | `1s` | Idle polling delay |
| `JOB_ATTEMPT_TIMEOUT` | `30s` (fake), `120s` (claude, openai) | Analyzer deadline; at least `60s` for real analyzers |
| `JOB_LEASE` | `60s` (fake), `180s` (claude, openai) | Fixed ownership duration |
| `JOB_DATABASE_TIMEOUT` | `5s` | Bounded storage operation |
| `JOB_RETRY_BASE` / `JOB_RETRY_CAP` | `2s` / `60s` | Capped exponential retry delay |
| `JOB_MAX_ATTEMPTS` | `3` | Claim limit, 1-100 |
| `WORKER_SHUTDOWN_TIMEOUT` | `10s` | Maximum wait for cancellation |

Durations must be between 1ms and 24h (fake delay may also be zero). The lease
must exceed the attempt timeout plus three database timeouts (claim, input load,
and completion), and the retry cap must be at least the retry base. These
WORKER_/JOB_ keys are optional for the API. A real analysis can take much longer
than the fake one, so `claude` and `openai` default to a 120s attempt timeout and a
180s lease, and reject an attempt timeout below 60s: a shorter deadline would cancel
calls that may already be billed and retry them.

## Real analyzers

Each provider is one adapter package that implements the worker's `Analyzer`
interface and a startup `Check`. The worker, the domain packages, and the API do
not depend on any provider SDK. `cmd/worker` chooses the adapter from
`WORKER_ANALYZER`; adding a provider means one adapter package and one case there.

Every adapter sends the shared prompt from `internal/analyzer/prompt`:
- the system prompt, the delimited user message, and the JSON schema mirroring the
  v1 contract;
- `prompt.Version` (currently `incident-analysis-v1`), stored with each result. A
  test pins the prompt text and schema, so any change requires a new version.

Every adapter also:
- verifies the key and model with a Models API lookup before the worker claims any
  job (nothing is generated or billed for the check); `-check-config` stays offline;
- turns its SDK's retries off, so the worker's bounded retry policy and lease are
  the only retry mechanism;
- ignores the provider's own environment variables (`ANTHROPIC_*`, `OPENAI_*`) for
  the key, endpoint, and account, so only `WORKER_AI_*` settings apply;
- records the model that actually answered as `model_name`;
- maps provider responses to the same worker outcomes (tables below).

A provider-config outcome (bad key, billing, access, model, or an effort the model
does not support) never fails or charges the job: the worker releases it to
`pending`, gives the attempt back even if it was the last one, reopens the incident,
and exits with a message naming the cause. If the startup check itself fails, the
message distinguishes a rejected configuration from an unreachable provider; Ctrl+C
during the check exits cleanly.

## Claude analyzer

`internal/analyzer/claude` uses the official Go SDK
(`github.com/anthropics/anthropic-sdk-go/v2`).

- **Request.**
  - One Messages API call per attempt, `max_tokens` 16000, effort from
    `WORKER_AI_EFFORT`.
  - The model's default adaptive thinking.
  - `output_config.format` set to a JSON schema that mirrors the v1 contract. The
    response is still decoded and validated with `analysis.Decode`.
- **Refusal fallback.** For models that support it (`claude-opus-5-5`,
  `claude-opus-5`, `claude-sonnet-5-5`, `claude-fable-5-1`, `claude-fable-5-2`),
  server-side refusal fallback is enabled (`fallbacks: "default"`, beta). The
  stored `model_name` is the model that actually answered.
- **Prompt.** The system prompt carries the blueprint's safety rules:
  - separate observations from suspected causes;
  - never claim a confirmed root cause;
  - do not invent alarm meanings, measurements, or previous incidents;
  - state missing information instead of guessing;
  - add safety notes for moving equipment and electrical work;
  - cite only the incident ID as evidence.

  The incident and its device (name, type, location) are sent as JSON inside
  `<incident_data>` tags and are treated as untrusted data, so instructions in
  descriptions or logs are ignored.
- **Error mapping.**

  | Provider response | Worker outcome |
  | --- | --- |
  | 408, 409, 429, 5xx (including 529 overloaded), connection failure | Transient; retried with backoff |
  | Attempt deadline | `ANALYSIS_TIMEOUT`, retried |
  | `stop_reason: refusal` | `ANALYSIS_REFUSED`, terminal |
  | `stop_reason: max_tokens`, no text, or output that fails validation | `ANALYSIS_INVALID_OUTPUT`, terminal |
  | 413, other client errors | `ANALYSIS_PERMANENT`, terminal |
  | 400, 401, 402, 403, 404 | `ANALYSIS_PROVIDER_CONFIG`: the job is released and the worker stops. Every request field except the model and effort comes from code, so a 400 means an unsupported configuration |

## OpenAI analyzer

`internal/analyzer/openai` uses the official Go SDK (`github.com/openai/openai-go/v3`).

- **Request.**
  - One Responses API call per attempt, with the shared system prompt as
    `instructions` and the delimited incident data as `input`.
  - `max_output_tokens` 16000, and `reasoning.effort` from `WORKER_AI_EFFORT`.
  - Structured output through `text.format` with a strict JSON schema
    (`strict: true`); the response is still decoded and validated with
    `analysis.Decode`.
  - `store: false`, so OpenAI does not keep the response for later retrieval.
- **Model.** There is no default: `WORKER_AI_MODEL` must name a model that supports
  the Responses API, structured outputs, and the configured reasoning effort.
- **Error mapping.**

  | Provider response | Worker outcome |
  | --- | --- |
  | 408, 409, 429 rate limit, 5xx, connection failure, a `failed` response | Transient; retried with backoff |
  | Attempt deadline | `ANALYSIS_TIMEOUT`, retried |
  | A refusal in the output | `ANALYSIS_REFUSED`, terminal |
  | An `incomplete` response (token limit), no text, or output that fails validation | `ANALYSIS_INVALID_OUTPUT`, terminal |
  | 400, 413, other client errors | `ANALYSIS_PERMANENT`, terminal |
  | 401, 402, 403, 404, 429 `insufficient_quota`, or a 400 about the model or reasoning effort | `ANALYSIS_PROVIDER_CONFIG`: the job is released and the worker stops |

Tests use a local `httptest` server in place of each provider; automated tests
never call Anthropic or OpenAI.

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
`SchemaVersion` is 1. Trusted metadata (source, model, prompt version, IDs,
timestamps) is not part of the result JSON; the application and database assign
it. `analysis.Provenance` validates it: `fake` has no model or prompt version, and
`claude` and `openai` require both. The contract permits the full confidence range; the fake
analyzer always uses 0.

## Storage

Migration `00004_create_incident_analyses.sql` stores at most one immutable result
per incident: an application-generated UUID, schema version 1, a trusted source,
a JSONB result, and a database-generated timestamp. Completed jobs from before this
table existed keep no result and are never given a fabricated one.

Migration `00005_add_analysis_provenance.sql` allows the sources `fake` and
`claude` and adds `model_name` and `prompt_version`. A check constraint requires
both to be null for `fake` rows and non-blank for `claude` rows. Existing fake rows
are unchanged, with nothing backfilled. Its Down migration fails while `claude`
rows exist, so provenance is never silently discarded.

Migration `00006_allow_openai_analyses.sql` adds the source `openai` and requires a
model and prompt version for every non-fake source. Its Down migration fails while
`openai` rows exist.

`JobRepository.FinishWithResult` validates the provenance, locks the job, checks
the claim token and lease, validates the result against the incident ID read from
the locked row, inserts the result with its provenance, and completes the job in
one transaction. A failed insert, completion, or
commit leaves no partial state. A lost commit acknowledgement can still mean the
transaction committed, so callers rely on the existing lease recovery. Successful
jobs complete only through this operation.

Stored results are returned by incident detail as the
[`analysis` field](API-Reference.md#analysis), after being decoded again.
