---
title: Live worker checks need a second API on the worker test database
author: Knyongki
date: 2026-11-14
branch: main
cost: { minutes: 1, tokens: 0 }
id: l-8ed06869
scope: repo
status: active
src: agent
touches:
  - docs/Local-Automation-Setup.md
  - scripts/start-worker.ps1
  - "internal/worker/worker.go#Policy.Validate"
---

## Decided
To check the fake worker end to end, create incidents through a second API process that uses the separate worker test database. The normal API writes to the development database, which the worker never reads, so its incidents stay "queued" forever. This isolation is deliberate: the fake worker must not consume the normal development queue.

In the code:
- Worker: `scripts/start-worker.ps1` (run `-MigrateOnly` first), reads WORKER_DATABASE_URL (a localhost database ending in _worker_test).
- Second API: the child-process command in docs/Local-Automation-Setup.md ("Isolated fake worker") sets DATABASE_URL=WORKER_DATABASE_URL and HTTP_ADDRESS=127.0.0.1:18082.
- Postman: set collection `base_url` to http://127.0.0.1:18082, run Incidents - Manual starting with "Create Incident Test Device" (devices from the normal database don't exist there), then set it back to 8080.

## Tried and ruled out
- Using `scripts/start-local.ps1` as the API for the live check: wrong database, so the worker never sees the jobs.
- Setting WORKER_FAKE_OUTCOME in the terminal: private .env values override terminal values in the local scripts, so change it in .env and restart the worker.

## Watch out
- "queued" right after a mode switch usually means the incident was created before the restarted worker came up; compare created_at with the worker start time before debugging.
- Get Created Incident accepts any analysis_status, so a green Postman run does not prove the worker ran; read the value. "failed" alone does not distinguish invalid output from a permanent failure; the error code appears only in the worker log.
- Policy.Validate requires JOB_LEASE > JOB_ATTEMPT_TIMEOUT + 3 x JOB_DATABASE_TIMEOUT (claim, input load, completion); custom .env timeouts that met the old two-timeout rule can now stop the worker at startup.
