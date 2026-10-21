# Workbench connector configuration

This package makes real, read-only Git requests and real configured HTTP model requests. It has no Aliyun integration and no synthetic fallback. Package tests use only temporary repositories and local HTTP test servers; tests do not load runtime credentials or contact external services.

## Source repositories

The workbench operator supplies `SourceConfig.Repositories`, a map from stable repository ID to canonical absolute local checkout path. Resolve filesystem aliases when preparing configuration (for example, macOS `/var` resolves to `/private/var`). Paths containing symlinks are rejected. The connector copies the map at construction. A missing repository map produces an unready connector; calls return `ErrUnconfigured`.

```go
source, err := connectors.NewSource(connectors.SourceConfig{
    Repositories: map[string]string{"mobile": "/srv/workbench/repos/mobile"},
    AllowedExtensions: []string{".kt", ".java", ".swift", ".go"},
})
```

The caller must authorize project access and select an administrator-created build mapping before calling `Read` or `Diff`. Mapping creation must validate the repository and commit against operator configuration. A fragment's `Verified` means that the fragment came from a regular blob at the exact immutable commit; it does not independently prove a build/version association. The backend supplies that association using its persisted trusted mapping. Never accept a repository path/URL from an API request or infer mappings from branch names, source filenames or issue context.

`ValidateCommit(ctx, repoID, commit)` validates repository allowlisting, hash shape, object existence and actual commit type before the backend persists a trusted mapping.

`Read(ctx, SourceRequest{RepoID, Commit, Path, Start, End})` accepts lowercase full SHA-1 (40 hex) or SHA-256 (64 hex) commit object IDs. A matching non-commit object is rejected. Branch names, tags, abbreviated hashes and options are rejected. Paths must be normalized repository-relative paths. Traversal, `.git` components (case insensitive), backslashes, control separators and option-looking components are rejected. Tree traversal rejects symlinks and submodules in the requested historical commit. Extensions must be allowlisted. Defaults allow common source/text extensions; sensitive or unrelated files such as `.env`, `.pem` and binary assets are excluded. Operators should narrow extensions for each installation.

`Start` defaults to 1; omitted `End` uses the configured line window and clamps at EOF. Out-of-range starts and reversed/oversized ranges fail. `Fragment` records repository ID, exact commit, path, actual line bounds, content, SHA-256 content `Hash`, Git `BlobHash`, and `Verified`. No working-copy content is used.

`Diff(ctx, DiffRequest{RepoID, LeftCommit, RightCommit, Path})` compares the same allowlisted regular file at two exact commits and records both commits plus a SHA-256 output hash. Both commits must contain the file. External diff drivers and text conversion are disabled. Git runs with a cleaned environment, replacements disabled, literal pathspecs and no credential prompts. Command errors are sanitized; local filesystem paths and subprocess stderr are not returned.

Defaults: 5 seconds per source operation, 1 MiB per file, 128 KiB output, 200 selected lines. Configuration cannot exceed 30 seconds per source operation, 4 MiB per file, 1 MiB output or 1000 lines; values above these caps fall back to defaults. Every Git operation receives the caller's cancellation context. Keep repositories under operator control; do not permit tenants to replace their contents or configuration.

## Model provider

Configure the complete OpenAI-compatible chat completions URL and provider model name. An API key is optional for local providers. Load production keys from the operator's secret mechanism; never commit keys or return connector configuration in workspace/session responses.

```go
model, err := connectors.NewModel(connectors.ModelConfig{
    Endpoint: "https://model.example.invalid/v1/chat/completions",
    Model: "operator-selected-model",
    APIKey: secretFromOperator,
})
```

Empty configuration creates an unready connector returning `ErrUnconfigured`. Partial or invalid configuration returns `ErrInvalidRequest`. HTTPS is required except for localhost/loopback HTTP. Endpoint userinfo, query parameters and fragments are forbidden. Redirects fail, so keys and evidence cannot be forwarded by a redirect. The key is sent only as an Authorization bearer header.

`Investigate(ctx, Investigation{Question, Evidence, PriorRounds})` serializes only the supplied immutable snapshot and optional historical rounds. `Evidence` has `ID`, `Kind`, `Label`, `Content`, `Verified`, `Availability`, `Status`, `OriginRef`, and optional typed `Provenance`. Status is `matched`, `candidate`, `manual`, or `unknown`; absent status/availability becomes unknown. `SourceProvenance` preserves trusted mapping/repository/app/platform/version/build identity, immutable commit, path and line bounds, supplied content SHA-256, Git blob hash, optional event/asset IDs and IdentitySource. `operator_declared` identity is an operator assertion and must never be described as authenticated SDK/CI or deployment attestation. The source status and proof must come from server-validated snapshot data, never from model output. `PriorRound` has `Question` and `Answer`. The caller is responsible for snapshot persistence, access control, selecting prior rounds and retention; the connector does not read mutable issue state or collect additional data. Historical citations absent from this request's evidence cannot support new claims.

Manual evidence always remains unverified. A source marked verified/matched is conservatively downgraded to candidate unless it is available and has a complete mapping/build proof, full commit/blob IDs, valid line bounds and SHA-256 matching the supplied content. When applying masking, preserve the original source/snippet hash in OriginRef and record the masked payload hash separately in Provenance.ContentHash and the retrieval manifest. The prompt explicitly forbids candidate/manual/unverified source from confirming incident code, deployed build or root cause; matched code does not by itself establish causality or a successful fix. Historical answers are context rather than new facts.

OriginRef accepts only JSON-compatible metadata, copied before request encoding. Metadata is limited to 16 KiB and depth 8 per evidence, at most 128 map keys or 512 array entries per node and 4096 visited nodes. Cycles, custom marshalers and unsupported values fail. Metadata, typed proof, evidence text, question and prior rounds all share the cumulative encoded-request budget. Over-budget requests fail before the HTTP call; raw string preflight prevents oversized snapshots from first allocating an unbounded JSON buffer.

The provider receives system instructions, a JSON user message and `response_format: {"type":"json_object"}`. It must return a standard `choices[0].message.content` JSON string containing exactly:

```json
{
  "summary": "Evidence-based investigation summary",
  "claims": [
    {"type": "fact", "text": "Observed behavior", "citations": ["evidence-id"]}
  ],
  "unknowns": ["Unresolved question"],
  "nextSteps": ["Suggested check"]
}
```

All four fields are required. Types are `fact`, `hypothesis`, `inference`; every claim needs nonempty text and one or more exact IDs from the current supplied snapshot. Unknown IDs, absent citations, extra fields, empty summary, malformed JSON, provider truncation and non-2xx status fail visibly. Empty claims are valid when evidence is insufficient. Citation validation checks identity and structure; it does not establish that a model's interpretation is correct. Users must review hypotheses and record separate verification evidence.

Defaults: 45-second timeout, 256 KiB total encoded request, 256 KiB response. Hard configuration caps are 2 minutes and 2 MiB each; larger values fall back to defaults. Evidence is limited to 500 items and prior rounds to 20. Caller cancellation and deadline errors propagate. Provider bodies, URLs, credentials and transport errors are never included in returned error messages or package logs. The package emits no logs.

## Backend error handling

Use `Ready()` for capabilities. Use `errors.Is` on `ErrUnconfigured`, `ErrInvalidRequest`, `ErrUnavailable`, `ErrLimitExceeded`, `ErrInvalidResponse`, `context.Canceled` and `context.DeadlineExceeded`. Surface connector failure on the source resolution or turn. Do not persist an answer on failure, silently substitute another model, or mark an unavailable source as version matched. Aliyun collection remains separately deferred/unconfigured.

## Native and Web import protocols

`NewProtocols(ProtocolConfig{NativeKey, NativeIV, WebPrivateKey, EnableWeb, MaxInputBytes, MaxPlainBytes, MaxLineBytes, MaxRecords, TempDir})` uses the existing `internal/protocol` decoder with stricter import validation. `NativeReady()`, `WebReady()` and `WebEncryptedReady()` report Native, Web v0 and Web v1 capabilities independently. Keys are byte slices loaded by the operator; no example contains a usable production key. Empty configuration remains unconfigured. Native requires a 16-byte AES key and 16-byte IV. Web v0 requires `EnableWeb`; Web v1 requires an operator-supplied RSA private key in PKCS#1/PKCS#8 PEM/DER format. Configuring the RSA key also enables Web.

`Decode(ctx, "native"|"web", raw)` returns atomic `[]ProtocolEvent` containing ordinal `Line`, original decoded `Content`, `LogType`, millisecond `LogTime`. The backend creates new event IDs in the asset/parse namespace and must preserve the original asset hash; these imports do not rewrite legacy IDs or legacy records. Do not use plaintext/text fallback for declared protocol input. Disabled formats return `ErrUnconfigured` even when the content resembles text.

Native imports validate contiguous complete SDK frames, decrypt through `NativeDecoder`, require complete gzip members via `DecompressStrict`, and validate bounded UTF-8 native JSON records. Prefix recovery, ignored frame noise, incomplete trailers, damaged gzip checksums, malformed records and invalid UTF-8 fail the complete decode. Transient decrypted plaintext uses a mode-0600 temporary file, removed on every success/error path. Configure `TempDir` on the workbench's restricted runtime filesystem.

Web imports accept either the SDK upload JSON object containing `logArray` or the raw comma-delimited `logArray` value. The report's app/device metadata does not automatically become trusted asset metadata. The adapter validates every envelope and base64 payload, known versions, required fields and numeric detail identities before delegating to `WebDecoder`. Unsupported versions, ignored/empty records, invalid base64 and trailing separators fail atomically. AES-CTR Web v1 has the original protocol's confidentiality properties; it is not an authenticated transport and syntactic validity does not independently establish a sender's identity.

Defaults: 32 MiB encrypted input, 64 MiB decoded plaintext, 1 MiB per record, 100000 records. Hard configuration caps: 64 MiB input, 128 MiB plaintext, 4 MiB record and 100000 records; larger/invalid values revert to defaults. Caller cancellation propagates. Protocol and crypto errors are sanitized to named connector errors. No plaintext or key is logged.

## Read-only legacy task adapter

`NewLegacy(LegacyConfig{BaseURL, Token, Mappings, Timeout, MaxResponseBytes})` reads existing legacy HTTP APIs using GET only. `BaseURL` is the trusted deployment context URL (for example `https://legacy.example.invalid/logan-web`, with no trailing `/logan`). HTTPS is required except loopback HTTP. Userinfo, query parameters, fragments, unsafe token headers and redirects are forbidden. The adapter never invokes upload, parser, collection, migration or data mutation routes.

Each operator-created `LegacyMapping` has `ProjectID`, `Kind` (`native` or `web`), exact positive `TaskID`, `AppID`, `Platform`, `DeviceID`, and for Web, exact `WebSource`. Mappings are limited to 100. No global latest/search discovery is attempted. Native task responses must match configured task ID, app ID, numeric platform (as a canonical decimal string) and device ID. Web mappings require platform `web` and match exact device and WebSource. Legacy Web does not supply an app ID: the operator must establish the trusted AppID↔WebSource association during mapping review. Unmapped or insufficiently scoped records remain unavailable.

`Ready(projectID)` reports that project's configured mappings. `List(ctx, projectID)` returns only mapped, scope-validated tasks. Any mapped scope mismatch fails the result with `ErrUnavailable` and returns no partial task data. `Detail(ctx, projectID, kind, taskID, detailID)` revalidates task scope, then reads the original detail and checks that both returned IDs match. No unscoped request is sent for an unknown project/task. Backend endpoints must still authenticate the actor and authorize project membership before calling this adapter.

`LegacyTask` contains `Ref`, trusted AppID, platform, device ID, available version/build, status and date. `LegacyDetail` contains `Ref`, original content, log type and time. Each `Ref` serializes as `legacyRef` and preserves `kind`, decimal string `taskId`, optional decimal string `detailId`, and a SHA-256 source response `sourceRevision`. Native 123 and Web 123 remain distinct; detail IDs are never reallocated. Reading this adapter does not snapshot content automatically: persist the returned revision and content when adding investigation evidence, and report a changed revision instead of overwriting historical evidence.

Default total call deadline is 5 seconds (hard configuration cap 30 seconds); response budget is 1 MiB per legacy response (hard cap 4 MiB). Legacy response/error bodies and credentials are not returned on failure or logged. Disabled legacy configuration returns `ErrUnconfigured`.
