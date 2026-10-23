# SDK upload → workbench implementation plan

> **For agentic workers:** Use subagent-driven-development for scoped implementation and independent review. User approved this chain in the current request; execute without further design approval. Keep the current approved UI and legacy service intact.

**Goal:** Accept authenticated Android/iOS native SDK files directly into durable workbench assets and parse runs so the existing reader, source, evidence, investigation and handoff tools can use them without manual file import.

**Architecture:** Add an opt-in native SDK endpoint to the independent workbench service. Application-scoped ingestion credentials bind project/app/environment on the server and grant no workspace reading or ticket mutation. Reuse the strict Native decoder and existing atomic store/parse workers; retain originals, statuses and replay receipts. Mobile integration is additive: Android uses its existing custom-header API; iOS gets a backward-compatible headers overload. The old upload endpoint, database, IDs and LoganSite stay unchanged.

**Tech stack:** Existing Go net/http workbench, actual CLogan output, Android Java and iOS Objective-C SDK, embedded ES modules.

## Contract and constraints

- Endpoint: `POST /logan-web/api/workbench/v1/sdk/native`, raw encrypted SDK body. Header `X-Logan-Upload-Token` is an application upload credential, never a workspace/admin token. Optional `X-Logan-Upload-ID` is a bounded stable identifier for retries.
- `Config.SDKClients []SDKClient` (`json:"sdkClients"`); SDKClient fields `id`, `token`, `projectId`, `appId`, `environment`, `platforms` (android/ios). Native decode key/IV uses the existing operator configuration. Missing/invalid configuration fails closed; no secrets in capabilities or the repository.
- Original SDK headers: `appId`, `deviceId`, `platform` (1 Android / 2 iOS), `fileDate`, `appVersion`, Android `buildVersion` or iOS `bundleVersion`. Device/date required; app/platform contradictions rejected. Project/app/environment derive from configured credential, not caller project headers. Device/build remain SDK-reported assertions, not device/CI attestation.
- SDK input budget 32 MiB maximum (bounded opt-in config); manual import limits stay unchanged. Two active imports/parses remain bounded. No ZIP/text fallback for native input.
- Upload accepted means original asset + receipt + queued parse are committed atomically, not parsed successfully. Reply HTTP 200/code 200 with receipt/asset/run references and current status. Retry identity binds client/device/date plus complete metadata and original hash; same upload ID with changed content conflicts. Replays never revive expired data.
- Failed parse retains original + diagnostics, supports existing new-run reparse. Restart never leaves an unobservable queued job. Native raw downloads and exports remain independently authorized.
- No automatic issue association from client-supplied issue IDs. Logs arrive independently; explicit workbench linking uses the existing identity-difference confirmation and history. No automatic AI run or ticket write.
- Preserve layout/navigation, original browser and server data, SDK old public signatures and old Java/Go compatibility routes. Aliyun stays disabled.

## Tasks

- [x] **Backend:** add SDKClient config and native intake handler, receiver capability, provenance/receipt visibility, automatic parse creation and durable replay. Tests first: upload-only credentials cannot read workspace, project/app/platform spoofing rejected, strict Native parsing, duplicate/conflict, failure/reparse, retention, restart and atomic capacity failure. Own new `internal/workbench/sdk_ingest*.go`, relevant `server.go` and capability/expiry hooks, `cmd/workbench/main.go`.
- [x] **Mobile SDK integration:** add `loganUploadWithHeaders` without changing `loganUpload`; use existing Android custom-header upload. Provide application-scoped header integration examples without embedded secrets; verify via existing SDK tests/build paths. Own iOS wrapper/header and mobile integration examples/tests.
- [x] **Workbench presentation:** show SDK origin, original upload/parse status, device/build assertion source, and original encrypted file; retain reader/library/source/evidence/verify/handoff interactions and explicit issue linking. Tests first; no CSS or navigation redesign. Own embedded UI and related frontend tests.
- [x] **Integration/review:** actual CLogan generates encrypted bytes → real HTTP receiver → durable parse → same workbench API → explicit issue/evidence → manual validation/handoff; malformed input, duplicate, wrong scope and restart recovery. Run full Go, frontend and original prototype checks. Independently review credentials, replay, identity and expiry. Save evidence and browser results separately from true-device/real-provider integration, which requires the actual application's configuration.

## Acceptance

The new web library must show an SDK-uploaded log without choosing a local file. The reader must display the exact SDK content and provenance after parsing; replays cannot duplicate assets. It must be possible to explicitly associate it with a problem, add evidence and save a manual internal update. Source/model absence remains explicit. Actual physical-device and production cloud acceptance is not inferred from the local CLogan/HTTP flow.

## Completion evidence

See [SDK ingestion delivery and acceptance](../../product/2026-10-21-logan-workbench/sdk-ingestion.md). Actual CLogan/HTTP/browser and process restart were verified on an isolated synthetic project. Physical devices and real source/model/push providers remain explicitly outside this local acceptance. Independent review findings on project-bound replay and Android hostname validation were corrected and regression-tested.
