# Internal push and callback V1 design

User approved defining a provisional internal-service protocol on 2026-10-26: implement platform-owned protocol now, replace its adapter later. This design does not claim compatibility with a preexisting service or actual Aliyun delivery.

## Scope and approach

Use the existing collection draft wizard, durable state, generic API authorization/idempotency/revisions and layout. Add an opt-in internal HTTP push adapter, persisted collection/attempt/history, authenticated callback ingestion and live status reading. No direct Aliyun SDK integration. Existing uploads/parsing/manual investigation, projects, data and unrelated workspace edits stay intact; AI/source mapping remain deferred. No commit/push or real provider call in this task.

Alternatives considered: synchronous response-only loses delayed results; direct Aliyun integration violates the internal-service boundary. Chosen: asynchronous accepted job plus signed callback, with durable facts on our side. HTTP test doubles establish protocol behavior only.

## Provisional external protocol owned by the platform

Operator config file via WORKBENCH_PUSH_CONFIG_FILE: endpoint (full POST URL), token (outbound bearer >=32 chars), callbackURL (full absolute URL ending in our callback path), callbackSecret (independent HMAC key >=32 chars), projects (allowlist), deadlineSeconds (default600, range60..86400). HTTPS required except numeric loopback/localhost HTTP for isolated testing, no URL credentials/query/fragment or redirects. No URLs/secrets accepted from UI. Missing config keeps sending disabled. Config is private regular 0600, owned by process user, no symlink path components; reject unknown/duplicate project grants.

Internal service receives POST at configured endpoint; Authorization Bearer token and Idempotency-Key=attemptId. JSON:

```json
{"schemaVersion":1,"requestId":"opaque-platform-id","attemptId":"opaque-attempt-id","projectId":"project","app":"com.hxl.xlb.test","target":{"type":"device","value":"explicit-push-target"},"context":{"issueId":"optional-issue","title":"purpose","begin":"optional-ISO","end":"optional-ISO"},"callbackURL":"https://platform/logan-web/api/workbench/v1/integrations/push/events","deadlineAt":"ISO-UTC"}
```

Begin/end are user-requested investigation context, not an invented mobile command. The internal service owns actual downstream command mapping. It must deduplicate identical attemptId and reject changed payload for the same ID. New attempt means deliberate repeat. HTTP202 response must contain schemaVersion1, status accepted and nonempty providerRequestId <=128 bytes. Response only means job accepted. Definitive HTTP4xx rejection may mark rejected; timeout/network/5xx/malformed response mean dispatch unknown (could have executed), never automatic repeat.

Our callback route: POST /logan-web/api/workbench/v1/integrations/push/events. No browser bearer authentication: use X-Logan-Push-Timestamp (Unix seconds, +/-300 seconds) and X-Logan-Push-Signature (lowercase hex HMAC-SHA256 of exact timestamp bytes + newline + exact body bytes). Independent secret, constant-time compare, body<=64KiB; no tokens/error bodies in history or UI. Sign transport time, not occurredAt. JSON:

```json
{"schemaVersion":1,"eventId":"unique-service-event","requestId":"opaque-platform-id","attemptId":"opaque-attempt-id","providerRequestId":"service-job-id","eventType":"push_succeeded","occurredAt":"ISO-UTC","reasonCode":"OPTIONAL_BOUNDED_CODE"}
```

Only push_succeeded and push_failed supported in V1. push_succeeded explicitly means downstream push channel accepted the send as confirmed by the internal service, not device delivery or log retrieval. Missing client/upload evidence stays unknown. Future adapters must map their actual semantics rather than relabeling an accepted job as sent.

Callback IDs scoped to this configured integration: same eventId+same body re-ack without applying twice; same eventId changed body rejected409. Validate request/attempt ownership and provider ID (including callback arriving before HTTP202); no project supplied by caller can select scope. Append new accepted observations atomically with state. Reply2xx only after durable commit or exact durable duplicate; persistence failure503. Reject forged/stale/malformed/oversized/unknown events. Late/cancelled/old-attempt and contradictory terminal push observations are retained with ignored outcome, not allowed to change current attempt; conflicting provider identity is rejected.

## Durable domain/API interfaces

POST /collections body {draftId,draftRevision}; investigator+ role, generic UUIDv7 idempotency, persisted real draft values (not presentation mask), full target/time/binding validation, project connector grant, optional issue still valid. Block identical active target/app/window within project. Commit collection and attempt before external side effects. Return201 collection with currentAttemptId and revision. No upstream call under global store lock. Pending work interrupted by restart becomes unknown without blind replay; acknowledge this in UI and allow explicit retry. Deadline defaults10min, independent of content retention. Scheduler must stop with server and must not write when closed/unhealthy.

GET /collections and /collections/{id}, /collections/{id}/attempts, /collections/{id}/events use project-scoped authorization. Include collections/pushAttempts/pushEvents in workspace, redacted as existing drafts. Collection fields: id,title,issueId,app,targetType,targetValue,begin,end,revision,currentAttemptId,status,deadlineAt. Attempt fields: id,collectionId,dispatchStatus,pushStatus,providerRequestId,deadlineAt,number. Dispatch values queued/sending/accepted/rejected/unknown; push unknown/succeeded/failed. Collection status pending_dispatch/waiting_push/waiting_logs/dispatch_unknown/push_failed/timed_out/cancelled. Callback success only waiting_logs; no received/completed status manufactured. Callback before HTTP response cannot be regressed by HTTP response or timeout.

POST /collections/{id}/retries body {confirmed:true,reason:nonempty}; If-Match collection revision; only ended/unknown/timeout attempts, creates new immutable attempt and deadline while retaining prior events. Explicit confirmation explains old send might still reach client; retry number max5. POST /collections/{id}/cancel requires revision, confirmed true and reason, records cancellation but cannot retract already delivered messages. No fresh side effect on repeated API key. Retain known push outcome after deadline/cancel. A bounded sweeper applies deadlines, does not infer a failed send from silence.

This increment does not invent automatic SDK-to-retrieval correlation: upload continues as an independent verified asset. Current callback route cannot set file-received or parse-success. UI explicitly shows waiting for log/uncorrelated upload limitation and provides existing import/reader route. Future upload correlation requires a separate client/platform contract, not guessing by latest device ID.

## Frontend behavior

Keep existing navigation, three-step wizard, three tracking tabs, existing drafts/import/investigation links. Capabilities.collections.configured enables explicit send on confirmation; draft save remains separate. Use stable submission key per send/retry/cancel attempt; lost response offers result lookup/reload rather than blindly resending. Server collections and drafts both visible. Current detail displays dispatch/push/client/upload separately, signed event history, deadline, failure/unknown and all attempts. Retry/cancel are actual persisted API actions with explicit UI confirmation. Existing 2-second refresh also polls while collections active; preserve selected project/request, wizard edits and existing reading/investigation.

## Acceptance

Tests cover unchanged unconfigured draft flow; real HTTP internal-service invocation; callback-before-response; forged/stale/same-ID-conflict/duplicate/cross-project; exact persistence after restart; lost HTTP response unknown; store failure; timeout/cancel/old attempt callback; no send under lock; bounded side effects and state; UI send capability, stable keys and stage labels. End-to-end acceptance uses localhost service double with real signed HTTP callbacks and temporary storage, separate from current runtime. Do not declare actual Aliyun/client receipt/files proven. Full Go harness, existing+new Web tests, affected Python helpers and old manual-V1 isolated acceptance required. Scoped independent review before completion.
