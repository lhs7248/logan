# Bounded synthetic load runner

Run from ServerGo. Uses only Go standard library; no k6 binary required.

```sh
go run -tags stdjson ./cmd/loadtest --base-url http://127.0.0.1:8080 --allow-test-target --duration 10s --qps 5 --concurrency 2 --output /tmp/health.json
```

Default `health` mode checks HTTP 200 and `message:pong` on `/ping`. This measures HTTP health handling only, **not business throughput or capacity**. The base URL accepts an origin or the existing `/logan-web` context prefix, without query, credentials or fragment. Redirects are never followed. `--allow-test-target` explicitly designates an isolated test environment, including for localhost; this is an operator assertion, not server-side isolation verification.

Business workloads require `--allow-writes --test-target-marker logan-load-test`, because a brief query may lazily parse and persist logs. Never point these at production. `read` alternates native brief and detail; `upload` sends the prebuilt native binary body; `mixed` cycles brief/detail/upload equally. The route allowlist is fixed in code. Expected business responses are HTTP 200 and JSON `code:200`, with nonempty brief pages containing positive IDs, detail `id` matching the requested fixture ID, or an upload result shaped as a relative `/logan/downing?name=...` URL; 501 placeholders, 503 readiness responses, missing fixture rows and business errors fail. This validates the exercised response envelope, not full Java compatibility or parsed-content equivalence.

Preseed a dedicated database/object store with synthetic tasks/details. Supply existing positive IDs; generation does not seed, discover, or falsely certify their provenance. Clean up the isolated environment after uploading; repeated uploads create rows and objects. Generate a body from the recorded Java synthetic vector:

```sh
python3 tests/load/generate-fixture.py --output-dir /tmp/logan-load-fixture --task-id 1 --detail-id 1
go run -tags stdjson ./cmd/loadtest --base-url http://127.0.0.1:8080 --allow-test-target --allow-writes --test-target-marker logan-load-test --mode mixed --fixture /tmp/logan-load-fixture/fixture.json --duration 30s --ramp 5s --qps 20 --concurrency 4 --queue 4 --timeout 2s --output /tmp/mixed.json
```

The binary is a real native framed AES/gzip synthetic sample using the test-only key/IV in `testdata/protocol/java-native-vectors.json`; configure the isolated server with those values. Do not supply real logs, credentials or arbitrary header maps. The runner validates frame structure and approved synthetic metadata, not decryption with the target's configured key. Input upload is capped at 1 MiB, fixture JSON 64 KiB, response body 1 MiB. Only the native upload route is covered.

Offer zero is scheduled immediately, followed by the ceiling of the integrated QPS curve (including ramp); at 1 QPS for 1 second there is one offer. Missing final ticks are reconciled into attempted/dropped counters without starting requests after the scheduling deadline. The scheduler has fixed workers and a bounded queue, with no goroutine per request. `--ramp` increases offered QPS linearly to the limit; concurrency remains fixed. Duration stops scheduling and starting queued work. Active requests drain for at most their per-request timeout; interruption cancels them immediately. `schedule_seconds` records the offered window; `elapsed_seconds` includes this bounded drain. Queue leftovers count as dropped. `attempted = sent + dropped`, `completed = success + errors = sent`. Latencies cover sent requests through response-body validation (including errors), in milliseconds; percentiles use sorted lower ranks. `achieved_rps` is successful completions per elapsed second. Reports contain aggregate counters and fixed error categories, never target URLs, query strings, headers, bodies or DSNs. Nonzero exit means errors, drops, zero successes, interruption (130), or configuration/report failure (2). Limits cap duration at 1h, timeout at 1m, concurrency at 1024, queue at 65536 and offered requests at one million, bounding retained latency samples.

For Java/Go comparison use the same runner settings, synthetic dataset size, protocol configuration, dependencies, machines, warmup and measurement duration against each isolated target. Keep their reports, server CPU/RSS/GC, database latency, object-store latency and client saturation evidence. Ratio of successful RPS alone does not demonstrate capacity; sweep QPS/concurrency, require low error/drop rates, compare p95/p99, and record the resource bottleneck. Health results cannot establish a business ratio. No default target QPS is a capacity claim.

```sh
go test -tags stdjson ./cmd/loadtest
go test -race -tags stdjson ./cmd/loadtest
```

Tests cover cancellation/hanging transports, response limits, logical failure status handling, fixture guards and scheduler drop accounting. Actual DB/OSS/SDK end-to-end capacity needs a separate isolated environment.
