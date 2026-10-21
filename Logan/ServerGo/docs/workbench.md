# Investigation workbench runtime

This is the independent `cmd/workbench` executable. It serves the embedded browser UI and `/logan-web/api/workbench/v1` on the same origin. It does not replace the compatible service, alter its routes, migrate legacy data, or send Aliyun messages.

## Run an isolated instance

Build with `go build -o /tmp/logan-workbench ./cmd/workbench`. Create an operator-owned JSON auth file outside the checkout and restrict it to mode 0600:

```json
{
  "projects": [
    {"id":"trial","name":"Investigation trial","demo":false,"modelEnabled":false},
    {"id":"demo","name":"Synthetic examples","demo":true,"modelEnabled":false}
  ],
  "tokens": [
    {
      "token":"REPLACE_WITH_A_RANDOM_SECRET_OF_AT_LEAST_16_CHARACTERS",
      "actor":"operator",
      "projects":{"trial":"admin","demo":"admin"},
      "permissions":["raw","export","source","model"]
    }
  ]
}
```

Set `WORKBENCH_AUTH_FILE`, `WORKBENCH_DATA_DIR` (a dedicated operator-owned directory), and optionally `WORKBENCH_ADDR` (default `127.0.0.1:8091`). Run the executable and open that address. Enter the token and project ID in the UI. The UI stores the session token/project in browser sessionStorage; business objects stay on the server. Close the browser session or sign out to clear the token. Configure TLS and gateway authentication when exposing the service beyond an isolated machine. The executable does not configure a gateway for the legacy service.

All API requests require a bearer token and project grant. `GET /session` can discover the first configured grant without an explicit project header. Roles are `viewer`, `investigator`, `reviewer`, `admin`; resolving additionally requires reviewer/admin and valid fresh fix verification. Raw downloads, exports, source access and model sending each require the corresponding separate permission. An administrator does not implicitly receive those grants. Auth configuration is loaded at startup: rotate/revoke by restarting with the updated operator file; active turns are interrupted on restart.

Writes use a UUIDv7 `Idempotency-Key` generated at the user's attempt. Future keys beyond five minutes and keys older than seven days are rejected. Same actor/project/operation/key and normalized JSON or multipart content replay the original operation, subject to current expiry and source access. Changed input is a conflict. Use `GET /operations?key=...` or `/operations/{id}` after a lost response; use `/imports?clientBatchId=...` to recover uploads. Mutable issue, context and editable-draft writes use `If-Match` revisions. Manual issue status changes require `reason`; handoff summary records the human reason.

## Storage, bounds and recovery

The default durable file store uses an exclusive Unix advisory process lock and atomic, fsynced state replacement. A second runtime on the same directory fails to start. Business objects, original file bytes and idempotency receipts are committed together. It is a bounded single-writer trial store: the complete state is serialized on writes. It is not a horizontally scalable log database. Keep the directory on a local filesystem with reliable atomic rename, advisory locks and fsync; network filesystems have not been validated. Take access-controlled backups only when the runtime is stopped or using a consistent filesystem snapshot. Restore to a separate directory to avoid replacing live state.

Default bounds: 8 MiB/file, 16 MiB/file batch, 10 files/batch, 32 MiB HTTP body, two concurrent uploads, two concurrent parses, 100000 records/run, 1 MiB/plain record, 30 seconds/parse, two concurrent model calls and one active turn/issue. Total serialized business state is capped at 128 MiB, independently of the 256 MiB raw-file cap; over-capacity writes fail without publishing partial results. Config fields `MaxFileBytes`, `MaxBatchBytes`, `MaxEvents`, `MaxStateBytes` and `Retention` may be supplied by a trusted embedding application; the auth JSON also accepts Go field names. Durations are Go nanosecond integers in JSON. The shipped CLI intentionally uses these bounded defaults.

Content expires after seven days by default. Reads enforce expiry immediately; the startup/minute reaper removes original bytes and clears expired event/evidence/snapshot/answer/export/verification/history bodies. The reaper cancels turns whose input expired. IDs, hashes, timestamps and availability markers remain. Previews are rejected when their evidence expires or revisions change. An active provider reply is checked again before publication. Retry creates a new turn using the original snapshot, and expired snapshots cannot be retried. Interrupted turns become `interrupted` on startup; unfinished parses become failed with `PROCESS_INTERRUPTED`; no old worker can publish through the new runtime's lease.

Common credentials, authorization bearer values, email addresses and Chinese mobile numbers are masked in API presentation, source/legacy responses, exported investigation/event views and outgoing model input. Raw download is an independently granted original-file operation. The fixed `default-v1` masking policy is a baseline, not a certified detector for all application-specific personal data: approve and extend the policy for the deployment's data classification before enabling a real external model. The input manifest records original and actual sent hashes, sent bytes, exclusions, provenance and policy version. Source-dependent answers, verification text, hypotheses and handoff summaries are restricted again when source grants are unavailable, including cached operations and exports.

Optional MySQL persistence uses `WORKBENCH_MYSQL_DSN` and `WORKBENCH_NAMESPACE` (default `default`). An operator must first review and explicitly execute [001_state.sql](../sql/workbench/001_state.sql) against the intended isolated database. Startup never creates or migrates tables. This first adapter stores the complete workbench state and receipts in one transactional namespace row, takes a dedicated MySQL advisory runtime lease, and uses revision compare-and-swap. It is also single-writer per namespace. Losing the lease or commit failure stops serving data. Unit tests exercise SQL lease/conflict behavior; real MySQL deployment, failover and capacity have not been validated.

## Real connector configuration

Connector implementation and exact guarantees are documented in [connectors/README.md](../internal/workbench/connectors/README.md).

- `WORKBENCH_SOURCE_REPOS`: JSON map from operator repository ID to canonical absolute local Git checkout directory. An admin creates a per-project app/platform/build mapping to an existing full immutable commit. Path or line overrides remain candidates unless the actual event reports that path/line. Uploaded identity is explicitly `operator_declared`; agreement with a trusted mapping does not authenticate the incident's SDK or CI identity. Historical blob content, commit, selected lines and hashes are retained in evidence provenance. Repository roots and secrets are not returned through capabilities.
- `WORKBENCH_MODEL_ENDPOINT`, `WORKBENCH_MODEL`, optional `WORKBENCH_MODEL_API_KEY`: a real compatible HTTP provider. The endpoint must use HTTPS except loopback HTTP. Each permitted project must also set `modelEnabled:true` in operator configuration; the actor needs `model` and any required `source` permission. Missing configuration creates a failed turn with `CONNECTOR_UNCONFIGURED` and no answer. Provider errors, malformed structured responses, invalid citations and cancellation never become completed answers. Models never write tickets or execute instructions in uploaded data. Use manual investigation without a model.
- `WORKBENCH_NATIVE_KEY_BASE64`, `WORKBENCH_NATIVE_IV_BASE64`: explicit 16-byte key/IV for strict Native import. `WORKBENCH_WEB_PROTOCOL_ENABLED=true` enables Web v0 envelopes; `WORKBENCH_WEB_PRIVATE_KEY_FILE` enables encrypted Web v1 using an operator-owned RSA key file. Format signatures cannot bypass their adapters by renaming a file or declaring text. Original encrypted bytes and their hashes remain the uploaded asset; decoding creates independent new parse IDs and does not modify legacy records.
- `WORKBENCH_LEGACY_CONFIG_FILE`: JSON matching `connectors.LegacyConfig` (`BaseURL`, optional `Token`, `Mappings`). Every mapping supplies explicit `ProjectID`, `Kind`, original numeric `TaskID`, `AppID`, `Platform`, `DeviceID`, and Web `WebSource`. The adapter only reads exact mapped tasks. UI/API legacy reads preserve separate Native/Web task/detail decimal-string IDs and source revision hashes. It cannot enumerate unmapped legacy data or mutate old tasks. This initial bridge reads individual detail IDs; broad legacy search and automatic historical evidence migration are not implemented.

Aliyun remains explicitly unconfigured: `POST /collections` returns 503 `CONNECTOR_UNCONFIGURED`. No package name is treated as an AppKey, no push command is guessed, and no timer simulates delivery or file reception.

## Workflow and verification

Uploads become durable assets before a separate parse operation. Strict parsing publishes no events on any bad record; skip mode publishes valid records atomically and retains diagnostics. Reparse creates a new run and keeps prior event identities. Events, histograms and exports share run/filter hashes. Context and related-request candidates do not change the reader's original scope. Request IDs identify candidates and do not establish causation.

Evidence selection changes only future context revisions. Each turn fixes an immutable evidence snapshot, and manual source fragments stay unverified. Completed model hypothesis claims receive independent persisted IDs and versions; manual hypotheses can also be created. Hypothesis verification validates issue/version scope, stores observations append-only and adds its evidence to the next context. Fix verification is separate. Resolve requires the latest nonexpired fix verification to pass with nonexpired evidence, a build/scenario, reviewer authorization and a current awaiting-verification state; reopening invalidates earlier lifecycle proof.

Editable handoff text is saved independently before preview and survives refresh/restart. A preview fixes evidence, issue/context/verification revisions, a hash and version. Confirming it writes one immutable internal update; repeated keys replay it, while a second key cannot reuse the consumed preview. Stale or expired previews preserve their text and reject the write. This is an internal workbench ticket history, not an external issue-tracker integration.

Run `go test -race ./internal/workbench/...`, `go vet ./internal/workbench/... ./cmd/workbench` and `go build ./cmd/workbench`. Connector/model tests use only loopback HTTP servers and temporary Git repositories; they need local socket permission. The API persistence tests use actual temporary durable stores and restart them. SQL lease tests use sqlmock and do not substitute for real database acceptance. Existing repository full checks and browser acceptance remain separate evidence.
