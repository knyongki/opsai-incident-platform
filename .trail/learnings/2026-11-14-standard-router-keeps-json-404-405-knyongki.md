---
title: Standard router keeps JSON 404/405 only through explicit fallbacks
author: Knyongki
date: 2026-11-14
branch: main
cost: { minutes: 20, tokens: 150000 }
id: l-f3afbbca
scope: repo
status: active
src: agent
touches:
  - AGENTS.md
  - README.md
  - docs/API-Reference.md
  - docs/Local-Automation-Setup.md
  - docs/Postman-Quickstart.md
  - docs/Tasks.md
  - docs/Worker-and-Analysis.md
  - api/postman/OpsAI API/.resources/definition.yaml
  - api/postman/OpsAI API/Incidents - Manual/Get Created Incident.request.yaml
  - api/postman/OpsAI API/Incidents - Worker Result/.resources/definition.yaml
---

## Decided
The API uses Go's standard ServeMux with method patterns, but its defaults break the documented JSON contract, so three fallbacks are deliberate:
- Every path also gets a methodless pattern that writes the JSON 405 with the exact Allow value. Without it a catch-all "/" would win and turn 405s into 404s.
- A GET pattern also matches HEAD. API GET routes reject HEAD on purpose so behavior and Allow headers stay as documented; only /health and /ready accept HEAD.
- Paths that are not already clean (//health, /a/../health) get a JSON 404 before the mux, because ServeMux would answer them with an HTML 301 redirect.

## Watch out
Removing any of these "redundant-looking" handlers silently changes status codes or response format. The router tests cover each case; keep them passing unchanged.
The go vet failures during this change were only unused imports and a wrong test stub signature, nothing structural.

In the code: internal/transport/http/handler.go (router.route, cleanPaths), internal/transport/http/router_test.go (TestRouterContract).
