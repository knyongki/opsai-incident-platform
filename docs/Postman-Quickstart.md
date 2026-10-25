# Start OpsAI and test with Postman

Use this guide after completing the [new-device setup](Local-Automation-Setup.md).
Run commands from the project root in Windows PowerShell.

## 1. Start the project

Open Docker Desktop and wait for its engine, then run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-local.ps1
```

The script loads your private `.env`, starts/reuses PostgreSQL, applies pending
forward migrations, and starts the API. Wait for **API listening** and keep the
terminal open. With the example ports, PostgreSQL uses **5432** and the API uses
**8080**. You do not need to re-enter credentials each time.

## 2. Configure Postman

Open the local YAML collection **OpsAI API** from
[`api/postman/OpsAI API`](../api/postman/OpsAI%20API).

Set the collection variable `base_url` to `http://127.0.0.1:8080`.
If you changed HTTP_ADDRESS, use that port instead. Disable any active environment
that overrides `base_url` with a stale value. The incident request uses the
collection's dedicated `incident_device_id`.

## 3. Send requests in order

| Order | Request | Expected result |
| --- | --- | --- |
| 1 | System / Health | 200; `data.status = ok` |
| 2 | System / Readiness | 200; `data.status = ready` |
| 3 | Incidents - Manual / Create Incident Test Device | 201; saves `incident_device_id` |
| 4 | Incidents - Manual / Create Incident | 202; `status = open`, `analysis_status = queued` |
| 5 | Incidents - Manual / List Incidents | 200; summary items and cursor page |
| 6 | Incidents - Manual / List Incidents With Filters | 200; includes the created incident |
| 7 | Incidents - Manual / Get Created Incident | 200; full description/logs and job status |
| 8 | Incidents - Manual / Get Incident - Invalid UUID | 400; validation error |
| 9 | Incidents - Manual / Get Incident - Missing | 404; missing incident |

Inspect the test results after each request. The incident request checks status,
JSON content type, and the response envelope, UUID, and status values.

Run **Incidents - Manual** in order, or send its seven requests individually in that
order. It creates its own device, independent of the device CRUD folder and its
cleanup. After an incident references a device, deleting that device returns
**409 Conflict**. The cloud collection now includes the 3B read requests following
an explicitly requested sync. Refresh the collection to see them. Create Incident
saves `incident_id` for the detail request. Future edits still require explicit sync.

Incident requests create persistent records. Repeating POST creates another
incident and job. `queued` means the job is stored; no worker or AI runs yet.
HTTP assertions do not replace the repository transaction integration tests.

## 4. Stop

Press **Ctrl+C** in the API terminal. PostgreSQL stays running.
To stop it while preserving data:

```powershell
docker compose --env-file .env.example stop postgres
```

The startup command starts it again next time. Never delete volumes or run Down
migrations as routine cleanup.

For automated System tests instead of an interactive API, run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

That command uses a temporary API on port 18080 and stops it afterward.
See the [setup guide](Local-Automation-Setup.md#troubleshooting) if startup fails.
