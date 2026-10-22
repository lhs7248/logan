# Approved workbench design restoration implementation plan

> **For agentic workers:** Use subagent-driven-development for the frontend implementation and independent final review; execute scoped integration work in the parent task.

**Goal:** Restore the approved prototype's navigation, visual structure and investigation flow in the real server-backed client, then separately verify functional behavior and design fidelity.

**Architecture:** Reuse the approved prototype CSS as immutable baseline assets, port its view structure to safe DOM rendering, retain the existing authenticated API client and persistent backend. Do not run the prototype simulation/state scripts in the real application. Add durable unsent collection drafts so the approved Aliyun wizard survives refresh while real sending remains deferred.

**Tech Stack:** Existing Go service, embedded ES modules, existing Node tests and browser acceptance.

## Global constraints

- Design source: `a9b13c7`'s unchanged `prototypes/logan-investigation/{index.html,desk.js,desk.css,style.css,product.css,collection-flow.css,aliyun-retrieval.js}` and `docs/product/2026-10-21-logan-workbench/product-design.md`; prototype tree last changed on the already approved baseline before formal implementation.
- User authorized restoring the approved design and reaccepting it on 2026-10-22. No new product redesign is authorized.
- Retain sidebar, three primary entries, scope strip, judgment/next-step summary, switchable log library, left event reader/right six investigation tools, issue list and collection master/detail/tabs.
- Preserve current real data, original prototype/localStorage, auth/revision/idempotency/expiry checks, all current API functionality.
- Keep remote Aliyun sending disabled and explain why at the send action. Preserve its three-step parameter wizard, target semantics, unsent draft and tracking structure; do not create simulated receipts.
- Source/model require actual configuration. Candidate source does not authenticate incident identity. No synthetic answer is introduced.
- Rule changes already present in the working tree belong to the immediately preceding user request; preserve them.

## Task 1: Restore approved frontend structure

Files: existing `internal/workbenchui/static/app.js`, `style.css`, baseline CSS copies, `design-fidelity.test.mjs`.

- [x] Record baseline markup and create failing behavior tests for navigation/context preservation and baseline layout contracts.
- [x] Restore dark sidebar, breadcrumb/header/notifications, issue list, scope and summary, switchable library, reader/detail and six-tool layout using the prototype's existing class names and CSS.
- [x] Retain upload, parse history/comparison, filtering, source, evidence, multi-round AI, validation, autosaved drafts, export and history. Route parse and result tools in place, not generic replacement pages.
- [x] Give user-facing identity fields Chinese labels and put hashes/IDs/advanced source mapping in disclosure sections. Keep raw evidence inspection available.
- [x] Run all frontend tests, embedded Go routes and browser checks.

## Task 2: Preserve collection workflow without enabling push

Files: new `static/collection.mjs`, backend collection draft helpers, routes and tests.

- [x] Write a failing API test: create/edit/reload project-scoped unsent draft; no collection/request/receipt created; cross-project and stale revision rejected.
- [x] Implement `GET/POST /collection-drafts`, `GET/PATCH /collection-drafts/{id}` using existing server transaction/idempotency/revision guards, retain explicit `/collections` unavailable result.
- [x] Restore prototype master/detail list and three tabs: push response, files/results, failures/retry. Empty results remain empty; drafts are clearly unsent.
- [x] Restore wizard: app/device-account-alias parameters, optional issue/window, confirmation; durable save and disabled real send. Never infer Aliyun deviceId from ordinary issue device.
- [x] Test validation and actual server recovery, retain import as the available path back into the same investigation.

## Task 3: Independent design and behavior review

- [x] Review against original files, not against the new implementation. Check each primary page and tool, empty/failed/unconfigured states, context retention and existing abilities.
- [x] Fix material findings and retain unresolved differences as failed/not verified, never relabel the baseline.

## Task 4: Reaccept and deliver

- [x] Build final executable, run frontend/prototype regressions and affected Go tests/race/vet; run Harness check/self-test and HTTP acceptance.
- [x] In actual browser, compare baseline/implementation at the same desktop and narrow sizes with comparable synthetic data; save paired screenshots, record genuine environment/state differences.
- [x] Walk import/strict failure/reparse/read/source/evidence/AI/verification/writeback and collection draft recovery, record actual results and configuration gaps.
- [x] Write new acceptance matrix with separate functional/design conclusions; correct the earlier completion claim without erasing its historical record.
- [x] Preserve the running local instance and save code on current feature branch; no main merge/push.

## Final evidence

See `docs/product/2026-10-21-logan-workbench/design-restoration-acceptance.md` and its linked evidence index. Desktop comparison was 1440×1000; narrow comparison was 650×1000 only. Final frontend tests: 34; original prototype tests: 124. Harness full passed; after the final embedded UI-only fix, the UI Go package and final executable were rebuilt successfully. Browser creation of an empty issue was retested after the initially reproduced stale-log failure. Real push/source/model quality remain unverified, as explicitly recorded. No main merge or remote push is included.
