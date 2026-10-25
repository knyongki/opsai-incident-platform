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
| 3A - Incident creation and durable jobs | Finished | Implementation and verification passed; repository review remains |
| 3B - Incident list/detail (proposed) | Planned | Scope and API contracts require explicit approval |

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

## Milestone 3A - Finished
