# Embedded investigation client

The independent workbench executable serves this dependency-free client at `/workbench/`. The client calls only the same-origin `/logan-web/api/workbench/v1` API; it does not read or alter the prototype, legacy browser storage, or prototype state transitions.

The approved prototype's four CSS files are embedded as byte-identical `design-*.css` assets. The DOM follows its three navigation entries, switchable library, reader and six investigation tools. The integration stylesheet only adapts API-backed states and accessibility. Design acceptance is recorded separately in [the restoration report](../../../../docs/product/2026-10-21-logan-workbench/design-restoration-acceptance.md); API tests alone cannot establish design fidelity. Event clocks explicitly display Beijing time with milliseconds, and adaptive histogram bins retain server-side filter identity.

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
3. Choose a published run. Combine keyword, level, module and Beijing wall-clock date/time filters. The event page and histogram must have identical filter hashes; otherwise the client rejects the mismatch. Select a row to read its original fields, neighbor events and request candidates without changing the current filters. Load further pages when offered.
4. Select exact events and add them to the issue evidence. Add manual text separately. Remove and re-add evidence from the current range and inspect historical immutable snapshots. Download raw files or the filtered event export using an authorized token.
5. When legacy project mapping is configured, use the read-only historical task list and an explicit original detail ID; legacy references remain distinct from workbench events. A selected old detail can be explicitly saved as unverified manual evidence. Select a row and open **源码**. Use the configured immutable build mapping; missing file identity stays unknown. Inspect source status, fragment and comparison. Project administrators can register app/platform/build/repository/commit mappings. Missing configuration produces an explicit error and offers manual evidence.
6. Open **AI 排查**, inspect the next-round evidence scope and delta, ask a question, inspect each claim citation and the fixed snapshot. Cancel a live request or retry an ended attempt. Without a model, no answer is synthesized; manual investigation remains usable.
7. Create or select a stable hypothesis ID and version. Record hypothesis support/refutation/uncertainty separately from fix pass/fail. Fix verification includes build, scenario, expected and observed behavior and selected evidence.
8. Open **回写**. Enter a manual summary or choose a completed AI turn; edit owner/status and observe the autosaved server revision before generating a preview. Check its snapshot and explicitly confirm. Changes to issue, evidence or verification make the draft stale; edited text survives re-preview. Repeated submission uses the same generated UUIDv7 per draft and the server checks its stored receipt. Inspect immutable internal update history. Closing/reopening rules are enforced by the server.
9. Open **日志回捞**. Use the three-step wizard to enter explicit app and device/account/alias parameters, optional issue and investigation window, then review them. Durable actor-owned unsent drafts survive refresh. Ordinary issue devices are never substituted for Aliyun deviceId. Sending is enabled only when the internal push V1 connector is explicitly configured for this project; otherwise drafts remain available. Signed channel callbacks never imply client delivery or associated log receipt. Manual import and server-provided demo fixtures remain available from reading; demo loading is restricted by the server.
10. Reload and reconnect: saved issues, raw files, parse history, evidence, turns, verifications and internal updates must remain. Verify wrong project/token and viewer writes produce visible API errors, with a trace ID when supplied.

## Boundaries

This client is an API consumer. Model/source availability, revision enforcement, retention, cancellation, project isolation, source revalidation, export permissions and closing rules are enforced on every operation by the server. A hidden or shown button is not an authorization boundary. Editable draft text is durable only after the UI reports a successful autosave; unsaved or failed changes remain in memory and are explicitly marked. Source, evidence and turn body inspection re-fetches authorized current objects instead of opening cached excerpts. Idle sessions revalidate at most every 20 seconds, active jobs every 2 seconds. Unchanged passive updates do not rebuild the page or refetch immutable event pages; authorization and selected evidence remain revalidated. External Aliyun push is intentionally deferred and has no simulator in this client.


## Reading continuity (2026-10-26)

- `reader-state.mjs` owns issue filtering, normalized diagnostic summaries and browser navigation. User navigation creates history entries; passive updates replace the current entry. History holds scope, filters, loaded window size, selected event IDs and scroll positions, never tokens or log bodies; restoration revalidates session and event access.
- Event pagination retains the already loaded window. Changing project, run, filter or attempt rejects late replies from the former scope. Open details and unsubmitted inputs survive passive rendering.
- `GET /parse-runs/{id}/page-attempts` summarizes the complete authorized **parse run**, independent of filters and browser pagination. `summaryScope=entire_run` and per-attempt `matchingEventCount` explicitly distinguish lifecycle outcome from current filter matches. Stage links are bounded to the first and last 24 events per attempt; full event counts and terminal facts are retained. Stage links may include events outside the current filters; only missing normalized events in the complete run are described as not recorded.
- `pageAttemptId` is a shared event/histogram/export filter and is included in the cursor hash. The reader provides both current-attempt and all-base-filter exports. Changing this field invalidates old cursors.
- Structured rows use only server-normalized diagnostics; raw JSON is preserved in event details. Legacy/plain logs keep their original presentation.
- Evidence is the default investigation tool. Explicit historical URLs still select AI/source/other tools. Evidence offers direct manual judgment, verification and handoff actions; all six tools remain.
- Reproduce the HTTP scope checks with `python3 script/verify-workbench-reading.py --binary /absolute/path/to/workbench --report /tmp/reading-report.json`. It uses synthetic data and its own private temporary directory. Add `--serve` to retain that isolated fixture for browser inspection.

## Review fixes (2026-10-26)

- Independent reading distinguishes linking the current log asset from opening another issue. The former uses the existing log-links API and retains reading filters/window/selection; identity differences still require a reason and confirmation. Creating a new issue can explicitly link the current asset.
- Verification purpose changes rebuild compatible result choices, title and hypothesis fields, while preserving method/observation/evidence. Boolean hidden fields use DOM properties, not string attributes.
- Newer available manual updates appear in issue progress and the investigation summary. Exact repeated log selections reuse current evidence; simultaneous identical saves share one in-flight write.
- Import is file-first with optional identity metadata in a disclosure. Unknown identity stays blank; date inputs are Beijing wall-clock values converted to explicit +08:00 at the API boundary. Parsing/history has an explicit active location and return action. Unparsed assets have a stable `asset` URL parameter.
- Model/source operations are disabled with their actual capability/permission reason; evidence, verification and manual handoff remain available. Global status describes connection/last workspace sync; draft save state is separate. Transient failed sync retains the last readable data, while authorization failures still clear/revalidate content.
- The four approved baseline stylesheets and all three navigation entries/six investigation tools remain. Final whole-page browser comparison is a separate acceptance requirement; passing automated tests is not evidence of design approval.
