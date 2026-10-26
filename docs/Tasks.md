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
| 4A - Durable worker foundation | Finished | Fake worker, leases, recovery/retries, and validation passed; user verified completed jobs with incidents still open and accepted the review |

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

## Next step

Review the final changes and prepare the Milestone 4A commit; the user creates the
commit. No commit or push has been performed. Plan Milestone 4B separately after
that; later analysis/result work is not authorized by this status update.
