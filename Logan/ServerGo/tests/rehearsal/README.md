# Isolated cutover order rehearsal

From ServerGo:

```sh
python3 -m unittest discover -s tests/rehearsal -v
python3 script/rehearse-cutover.py --docker /absolute/path/docker --host unix:///dedicated/runtime.sock --allow-isolated-runtime --work-dir /private/tmp/logan-rehearsal-work-unique --go-image local-go-foundation:rehearsal --fixture-image python:3.12-alpine
```

Only a dedicated disposable Docker runtime is allowed. Images must already exist locally; the runner never pulls. Record immutable image IDs from the runtime alongside the report. Every run creates unique labelled containers and an internal network, publishes no ports, and removes only its own names. It does not read production configuration or contact DB/OSS. It never operates the original Java service or existing gateway.

The Java side is explicitly a **Python simulator**, and the gateway is a synthetic HTTP fixture. This verifies container stop/start order, maintenance closure, path rewriting, actual Go `/ping`, and the continuing `/readyz=503` release block. Go routing permits only diagnostic `/ping`; business routes remain closed. It does **not** prove real Java restart, business compatibility, real nginx reload, draining Java jobs, shared-data single-writer safety, OSS access, or a production cutover/rollback. No data is written, so data reconciliation is untested.

The report defaults to `/private/tmp/logan-rehearsal-report.md`. A failed assertion or unavailable image/runtime fails the command; a failure report describes the boundary reached. A passing report is topology evidence only and cannot satisfy the release runbook gates.

The fresh `--work-dir` must be beneath `/private/tmp` and shared into the VM at the same absolute path. The runner copies its fixture into this directory; no checkout bind is required. Only synthetic fixture/state files are readable by container UID10001; Docker configuration stays private. Socket aliases are resolved before rejecting default or non-Logan runtimes. Ambient HTTP proxies are disabled. Interrupts unwind ownership-checked cleanup; cleanup errors fail the run.

HTTP probes execute inside the fixture containers over the internal Docker network with proxies disabled; no VM port forwarding is required. Failed runs record the exception and owned-container state/logs before cleanup. Event timestamps use UTC.
