# Provisional internal push V1

This platform-owned provisional protocol is replaceable at the internal HTTP adapter boundary. It has not been validated against an existing internal service or Aliyun. A successful push observation means downstream push-channel acceptance only. Device acknowledgement, file receipt and parsing remain unknown; uploaded logs remain independent assets without automatic collection correlation.

Set `WORKBENCH_PUSH_CONFIG_FILE` to an absolute private regular file owned by the process user with mode 0600. Neither it nor any path component may be a symbolic link. Missing configuration disables sending and preserves drafts. Example (replace every example secret before use):

```json
{"endpoint":"https://push.internal.example/v1/jobs","token":"replace-with-independent-outbound-secret-32-or-more","callbackURL":"https://logan.internal.example/logan-web/api/workbench/v1/integrations/push/events","callbackSecret":"replace-with-independent-callback-secret-32-or-more","projects":["project-id"],"deadlineSeconds":600}
```

Projects must be distinct existing project IDs; unknown JSON fields are rejected. HTTPS is required except HTTP localhost or numeric loopback for isolated tests. URL credentials, query, fragment and redirects are prohibited. Deadline range is 60–86400 seconds. UI requests cannot configure endpoints or credentials.

The adapter POSTs JSON with `schemaVersion:1`, `requestId`, `attemptId`, `projectId`, `app`, `target:{type,value}`, `context:{issueId,title,begin,end}`, `callbackURL`, and UTC `deadlineAt`. Authorization is `Bearer <token>`; `Idempotency-Key` is the attempt ID. Deduplicate an identical attempt ID and reject changed payload. A deliberate retry creates a new attempt. Respond HTTP 202 with `{"schemaVersion":1,"status":"accepted","providerRequestId":"bounded-job-id"}`. This acknowledges a job, not push success. HTTP 4xx is rejection; timeout, transport errors, 5xx and invalid acknowledgements leave dispatch unknown. The platform never automatically resends an uncertain attempt.

POST callbacks directly to `/logan-web/api/workbench/v1/integrations/push/events`, independently of browser bearer authentication. Body limit is 64 KiB. Set `X-Logan-Push-Timestamp` to current Unix seconds (within 300 seconds). Set `X-Logan-Push-Signature` to lowercase hex HMAC-SHA256 using callbackSecret over exact timestamp bytes, one newline byte, and exact JSON body bytes. Sign transport time, independently of occurredAt.

```json
{"schemaVersion":1,"eventId":"unique-event-id","requestId":"platform-collection-id","attemptId":"platform-attempt-id","providerRequestId":"bounded-job-id","eventType":"push_succeeded","occurredAt":"2026-10-26T00:00:00Z","reasonCode":"OPTIONAL_CODE"}
```

Only `push_succeeded` and `push_failed` are supported. IDs are bounded to 128 bytes and reasonCode to 64 uppercase alphanumeric/underscore characters. Exact durable event duplicates return 200; changed bodies under an existing eventId return 409. Unknown ownership or conflicting provider identity is rejected. Late, cancelled, previous-attempt and contradictory observations are retained with outcome ignored. HTTP 200 is emitted only after durable commit or exact durable duplicate; a failed commit returns 503 and fences further writes.

Create with `POST /collections {draftId,draftRevision}` using the normal project scope, investigator permission and UUIDv7 idempotency key. Read collection, `/attempts` and `/events` under `/collections/{id}`. Retry and cancel require `If-Match` collection revision and `{confirmed:true,reason:"..."}`. Retry confirmation must acknowledge the old send may still reach the client. Five total attempts maximum. Cancellation cannot retract a delivered message. An interrupted queued/sending dispatch becomes unknown at startup without resend. Deadlines end tracking, preserving known push outcomes. Signed success produces waiting_logs, never received/completed.

Investigation context time is optional: omit both begin/end (stored as blank), or supply both valid ISO timestamps with end after begin. Account/alias targets require explicit binding confirmation; device targets do not require that checkbox. Purpose title is optional. Identical active or uncertain collection parameters cannot create another send; use confirmed retry for an uncertain attempt.

At most two outbound requests run concurrently, independently of file upload capacity. Queued work rechecks cancellation, expiry and shutdown before invoking the adapter. Collection routing data and dependent attempt/event reasons follow configured content retention through expiresAt tombstones; expired collections cannot dispatch or retry and subsequent callbacks cannot advance them. Callback events include server UTC receivedAt independently of service occurredAt.
