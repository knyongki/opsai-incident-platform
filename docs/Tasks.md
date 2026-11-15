# OpsAI tasks

Use this file for current status and active tasks. The
[project blueprint](OpsAI-Project-Blueprint.md) keeps the general milestone roadmap;
the [README](../README.md) describes the product, and the [API reference](API-Reference.md),
[worker and analysis guide](Worker-and-Analysis.md), and [setup guide](Local-Automation-Setup.md)
describe implemented behavior and operating instructions.

## Status overview

| Milestone | Status | Outcome or next step |
| --- | --- | --- |
| 1 - Go API foundation | Finished | Configuration, logging, health/readiness, graceful shutdown, and unit tests |
| 2A - PostgreSQL foundation | Finished | Compose, connection pooling, database readiness, Goose migrations, and sqlc |
| 2B - Device management | Finished | Device CRUD, validation, HTTP tests, and repository integration tests |
| 3A - Incident creation and durable jobs | Finished | Reviewed, verified, and committed as b9c4e4d |
| 3B - Incident list/detail | Finished | Read APIs implemented and reviewed; unit, integration, Postman, and live pagination checks passed |
| 4A - Durable worker foundation | Finished | Fake worker, leases, recovery/retries, and validation passed; user accepted; committed as e2e9d1c |
| 4B - Structured analysis results | Finished | Result contract, migration 00004, atomic worker persistence, detail `analysis` field, and Postman worker-result checks; accepted 2026-11-14 |
| H1 - HTTP hardening and test automation | Finished | ServeMux routing with the unchanged JSON contract, `X-Request-ID`, access logs, panic recovery, and `check-local.ps1 -Integration`; accepted 2026-11-14 |
| L1 - Main-database fake worker and incident lifecycle | Finished | Explicit `WORKER_DATABASE_TARGET=main` and incident status transitions (`analyzing`, `analyzed`, `analysis_failed`) in the job transactions; no migration; accepted 2026-11-14 |
| 4C - Real AI analyzers (Claude, OpenAI) | Finished | Shared versioned prompt, Claude and OpenAI adapters (official SDKs), provenance with migrations 00005 and 00006, worker release on provider-config errors, review fixes, and Postman cloud sync; verified with stand-in servers, free bad-key checks for both providers, and a live OpenAI run (gpt-5.4-mini: 3 synthetic incidents analyzed, Postman Worker Result 21/21, no incident text in logs); Claude not run live; accepted 2026-11-15 |

The full incident and AI workflows remain on the blueprint. Results come from the
fake, Claude, or OpenAI analyzer, labeled with `source`, `model_name`, and
`prompt_version`. Reanalysis, result history, review, resolution, similar-incident
retrieval, and AI tools need a new plan and an explicit request.
Lasting contracts and commands are in the [API reference](API-Reference.md)
(including [incident analysis](API-Reference.md#analysis)), the
[worker and analysis guide](Worker-and-Analysis.md) (including the
[analysis contract](Worker-and-Analysis.md#analysis-result-contract) and
[storage](Worker-and-Analysis.md#storage)), and the
[worker setup guide](Local-Automation-Setup.md#isolated-fake-worker).

No milestone is currently active or planned in this file.

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
