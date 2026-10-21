# Embedded investigation client

The independent workbench executable serves this dependency-free client at `/workbench/`. The client calls only the same-origin `/logan-web/api/workbench/v1` API; it does not read or alter the prototype, legacy browser storage, or prototype state transitions.

## Local verification

From `Logan/ServerGo` run:

```sh
node --test internal/workbenchui/*.test.mjs
node --input-type=module --check < internal/workbenchui/static/app.js
go test ./internal/workbenchui
```

The API tests cover scoped credentials, revisions, stable submission identities, UUIDv7 timestamps, explicit HTTP errors, shared filtering dimensions, immutable evidence deltas, published-run visibility, authenticated downloads, byte preflight and transfer/abort events, serialized draft autosave/conflict retention, and explicit evidence change/expiry/refutation classification. A subprocess test parses every JavaScript module as an ES module, matching browser syntax rules. The Go tests verify embedded routes, content security policy and missing-file failures. These tests do not prove browser behavior or real source/model/SDK integration.

## Operator and browser walkthrough

Start the independent server with its documented auth file and data directory, then open `/workbench/`. Enter a configured token and authorized project ID; project access is checked by the server. The project selector uses `/session`, and changing project clears selected investigation objects. The token is the only value stored in sessionStorage. Business data stays on the server; handoff edits automatically save to an actor-owned server draft after 450 ms and offer an immediate save action. The saved revision, active save, errors and unsaved edits are visible; leaving with unsaved edits raises the browser's unsaved-change prompt. A preview is a server object; if the server returns actor-owned `handoffDrafts` in the workspace the latest unused preview restores when opening its issue.

1. Open **问题调查**, create an issue and edit its context. Return to the issue list and search title, description or owner.
2. Import one or multiple files. Leave unknown identity fields empty; app/platform/build must describe the actual uploaded files. Select auto/JSONL/JSON/text and strict/skip-invalid parsing. Native and Web SDK protocol choices appear only when the corresponding server capability is configured; declare the original protocol during import. Web plaintext packet support does not imply encrypted packet readiness. The client inspects actual byte sizes and UTF-8/protocol boundaries before transfer, displays browser-reported byte progress and allows transport cancellation. A stable clientBatchId in the URL supports actor-owned result lookup after a lost reply or reload; cancellation never claims the server did not commit. Observe processing, cancel a live run, inspect line errors, create another run without overwriting history, and compare two ended versions of the same original asset.
3. Choose a published run. Combine keyword, level, module and ISO time filters. The event page and histogram must have identical filter hashes; otherwise the client rejects the mismatch. Select a row to read its original fields, neighbor events and request candidates without changing the current filters. Load further pages when offered.
4. Select exact events and add them to the issue evidence. Add manual text separately. Remove and re-add evidence from the current range and inspect historical immutable snapshots. Download raw files or the filtered event export using an authorized token.
5. When legacy project mapping is configured, use the read-only historical task list and an explicit original detail ID; legacy references remain distinct from workbench events. A selected old detail can be explicitly saved as unverified manual evidence. Select a row and open **源码**. Use the configured immutable build mapping; missing file identity stays unknown. Inspect source status, fragment and comparison. Project administrators can register app/platform/build/repository/commit mappings. Missing configuration produces an explicit error and offers manual evidence.
6. Open **AI 排查**, inspect the next-round evidence scope and delta, ask a question, inspect each claim citation and the fixed snapshot. Cancel a live request or retry an ended attempt. Without a model, no answer is synthesized; manual investigation remains usable.
7. Create or select a stable hypothesis ID and version. Record hypothesis support/refutation/uncertainty separately from fix pass/fail. Fix verification includes build, scenario, expected and observed behavior and selected evidence.
8. Open **回写**. Enter a manual summary or choose a completed AI turn; edit owner/status and observe the autosaved server revision before generating a preview. Check its snapshot and explicitly confirm. Changes to issue, evidence or verification make the draft stale; edited text survives re-preview. Repeated submission uses the same generated UUIDv7 per draft and the server checks its stored receipt. Inspect immutable internal update history. Closing/reopening rules are enforced by the server.
9. Open **日志回捞**. Aliyun must stay explicitly pending; there is no simulated send or fake progress. Manual import and server-provided demo fixtures are the available paths; demo loading is restricted by the server.
10. Reload and reconnect: saved issues, raw files, parse history, evidence, turns, verifications and internal updates must remain. Verify wrong project/token and viewer writes produce visible API errors, with a trace ID when supplied.

## Boundaries

This client is an API consumer. Model/source availability, revision enforcement, retention, cancellation, project isolation, source revalidation, export permissions and closing rules are enforced on every operation by the server. A hidden or shown button is not an authorization boundary. Editable draft text is durable only after the UI reports a successful autosave; unsaved or failed changes remain in memory and are explicitly marked. Source, evidence and turn body inspection re-fetches authorized current objects instead of opening cached excerpts. Idle sessions revalidate at most every 20 seconds, active jobs every 2 seconds. External Aliyun push is intentionally deferred and has no simulator in this client.
