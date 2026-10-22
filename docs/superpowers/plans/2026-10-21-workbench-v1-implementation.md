# Workbench v1 Implementation Plan

> **For agentic workers:** Use subagent-driven-development to implement isolated backend, connector, and frontend tasks; review the integrated branch before delivery.

**Goal:** Deliver a runnable, durable investigation workbench for uploads, parsing, reading, issues, source evidence, AI investigation, verification and internal ticket updates. Aliyun push remains explicitly unconfigured by user instruction.

**Architecture:** Add an independent Go workbench executable alongside the compatible legacy service, using the existing Go module and dependencies. Serve a modular, dependency-free web client from embedded assets on the same origin. All business objects and raw files persist on the server; the prototype remains unchanged. Connectors implement real configured source/model access; absence returns a named unavailable result, never a synthetic answer.

**Tech Stack:** Go standard HTTP server, existing GORM/MySQL for optional database persistence, durable local storage for isolated development, browser ES modules and CSS.

## Global Constraints

- Baseline: `fe18032`; existing Java, SDK protocols, legacy HTTP routes, historical IDs, and prototype storage remain unchanged.
- New API base: `/logan-web/api/workbench/v1`; response `{data,meta:{traceId}}`; error `{error:{code,message},meta:{traceId}}`.
- Authenticated project-scoped operations, role checks, revision conflicts, durable idempotency, bounded uploads/parses, immutable evidence and turn snapshots.
- No real Aliyun push or guessed client command. Unconfigured connectors return `CONNECTOR_UNCONFIGURED`.
- Source match requires a configured immutable build/commit mapping. Manual fragments are unverified.
- Uploaded data and credentials must not enter source control. Runtime storage is ignored and operator-configured.
- No production migration, external message, model request or push occurs merely by building/testing.

## Integration contract (shared by implementers)

All requests send `Authorization: Bearer <token>` and `X-Project-ID`. Writes send `Idempotency-Key`; revisioned writes also send `If-Match`.

`GET /session` returns `{actor,role,projects:[{id,name,demo}],projectId}`.
`GET /workspace` returns arrays `{issues,assets,parseRuns,evidence,turns,verifications,updates,notifications,sourceMappings}` plus `capabilities`. Raw files and credentials never appear in this response. Every object uses string `id`, `projectId`, `revision` where mutable, and ISO timestamps.

- Issue: `{id,title,description,owner,priority,status,context:{app,platform,environment,device,version,build,begin,end},revision,contextRevision,evidenceIds:[]}`. Status values `investigating`, `awaiting_verification`, `resolved`.
- Asset: `{id,name,origin,platform,device,version,build,rawHash,size,createdAt,expiresAt,issueId}`; unknown fields are null or empty, never borrowed from an issue.
- ParseRun: `{id,assetId,status,format,mode,eventCount,errorCount,errors:[{line,message}],createdAt}`. Formats `auto`, `jsonl`, `json`, `text`; modes `strict`, `skip_invalid_records`; states `queued,running,succeeded,partial,failed,canceled`.
- Event: `{id,runId,line,time,level,module,content,fields,file?,sourceLine?,requestId?}`; exact identifiers survive evidence selection.
- Evidence: `{id,issueId,kind,label,originRef,content,availability,verified,createdAt}`. Context changes never overwrite historical turn evidence.
- Turn: `{id,issueId,question,status,snapshot:{evidenceIds,evidence,contextRevision},answer:{summary,claims:[{type,text,citations:[]}],unknowns:[],nextSteps:[]},error?,createdAt}`. Missing model must not generate an answer.
- Verification: `{id,issueId,purpose,hypothesisId?,method,expected,observed,result,build,scenario,evidenceIds,createdAt}`. Purpose `hypothesis` or `fix`; result `supported,refuted,uncertain` for hypotheses and `pass,fail` for fixes.
- Update: `{id,issueId,summary,status,owner,mode,turnId?,snapshot,verificationIds,createdAt}`.

Routes:
- `GET /capabilities`, `GET /issues`, `POST /issues`, `GET/PATCH /issues/{id}`, `POST /issues/{id}/log-links` body `{assetId}`.
- `POST /imports` multipart `metadata` JSON `{issueId?,platform,device?,version?,build?,begin?,end?,origin?}` plus repeated `files`; `GET /assets/{id}/raw` downloads original.
- `POST /assets/{id}/parse-runs` body `{format,mode}`; `GET /assets/{id}/parse-runs`; `GET /parse-runs/{id}`; `POST /parse-runs/{id}/cancel`.
- `GET /parse-runs/{id}/events?query=&level=&module=&start=&end=&cursor=&limit=` returns `{events,total,nextCursor,filterHash}`; `GET /parse-runs/{id}/histogram` uses identical filters; `GET /events/{id}/context` returns bounded neighbor events; `GET /events/{id}/related` returns candidates with identity gaps.
- `POST /issues/{id}/evidence` body `{kind,label?,originRef:{runId?,eventIds?,mappingId?,path?,start?,end?},content?}`; `PATCH /issues/{id}/context` body `{add:[],remove:[]}` using contextRevision; `GET /evidence-snapshots/{id}`.
- `POST /source-mappings` admin body `{app,platform,build,repoId,commit}`; `POST /source-resolutions` body `{eventId,mappingId?,path?,start?,end?}`; `GET /source-fragments/{id}`; `GET /source-diffs?leftRef=&rightRef=`.
- `POST /issues/{id}/turns` body `{question,parentTurnId?,contextRevision}`; `GET /turns/{id}`; `POST /turns/{id}/cancel`; `POST /turns/{id}/retries`.
- `POST /issues/{id}/verifications` body verification fields above; `GET /issues/{id}/verifications`.
- `POST /issues/{id}/handoff-previews` body `{mode,turnId?,summary,status,owner,issueRevision,contextRevision,verificationRevision?}` returns draft with `{id,version,hash,...}`.
- `POST /issues/{id}/handoffs` body `{draftId,version,hash,confirmed:true}` using issue revision. Return immutable update; stale drafts reject without deleting their text.
- `POST /exports` body `{kind,runId?,issueId?,query?,level?,module?,start?,end?}` returns `{id}`; `GET /exports/{id}/content` authorized download.
- `GET /notifications`; `POST /notifications/{id}/read`; `GET /demo-fixtures`; `GET /demo-fixtures/{id}/download`; `POST /demo-fixtures/{id}/load` restricted to demo projects.
- `POST /collections` returns 503 `CONNECTOR_UNCONFIGURED`; workbench shows deferred Aliyun integration.

## Task 1: Backend and durable business workflow

- [x] Write behavior tests for auth/project isolation, restart persistence, idempotency and revision conflicts before implementation; observe failures.
- [x] Implement bounded file import, strict/skip parsing, atomic publication, cancellation/reparse/history and common filtering/context/export semantics.
- [x] Implement issues, evidence snapshots, source/AI adapter integration, hypothesis/fix verification, stale draft detection and idempotent internal handoff.
- [x] Provide local durable single-process store and optional MySQL adapter; startup must not perform production migrations. Fail closed on store or connector errors.
- [x] Run package tests with race detection and connect real HTTP tests to durable temporary storage.

## Task 2: Real source and model connectors

- [x] Test repository allowlist, immutable commit identity, source path boundaries, model cancellation, errors and citation validation.
- [x] Implement configured local Git repository read/diff and explicit trusted mapping; no arbitrary client repository URLs.
- [x] Implement configured HTTP model provider with bounded time/size, structured claims, evidence IDs and response validation. Missing configuration stays unavailable.
- [x] Test actual requests against a local HTTP test server without contacting external model services.

## Task 3: Formal web client

- [x] Implement API/session/error handling and navigation; no browser-local business store or mock state progression.
- [x] Implement issues, upload/parsing/history and log reading with matching filter/histogram/context/export controls.
- [x] Implement source/manual evidence, natural language investigation, snapshot display, cancellation/retry, hypothesis/fix verification and handoff preview/confirmation/history.
- [x] Preserve reading position and current issue across tool changes; render untrusted text safely; expose connector readiness and Aliyun deferral clearly.
- [x] Embed assets in the Go executable and run user-visible HTTP/browser acceptance.

## Task 4: Integrated verification and delivery

- [x] Review implementation against 19 requirements and acceptance scenarios, record exact connector/environment gaps rather than claiming production readiness.
- [x] Run backend tests/race/vet/build, 124 prototype regression tests, syntax checks, harness checks, and actual browser workflows including reload and reparse.
- [x] Add runbook, configuration example without credentials, migration/storage limitations, real acceptance evidence and feature coverage matrix.
- [x] Save the completed work on the implementation branch; do not merge/push or alter production without a new user instruction.

## Delivery evidence

See [implementation status](../../product/2026-10-21-logan-workbench/implementation-status.md) and [acceptance record](../../product/2026-10-21-logan-workbench/evidence/implementation-2026-10-21/README.md). All checked tasks refer to the bounded local implementation and stated verification; external provider quality, real MySQL and release readiness are explicitly excluded from completion claims.
