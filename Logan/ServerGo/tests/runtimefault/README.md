# Isolated container signal harness

`runtimefault_test.go` is an opt-in root-package test entrypoint because the real
application lifecycle is private to that package. It changes no production code.
Build a Linux test binary with `go test -tags 'stdjson runtimefault' -c`, copy it as
`runtimefault.test` into a Docker build context, and use this directory's Dockerfile.
For a read-only root filesystem, mount a writable `/tmp` tmpfs owned by the
container UID and override `TMPDIR=/tmp`; the scratch image defaults to `/`
only for controllers that allow root filesystem writes.
The binary must run as container PID 1. Set `LOGAN_RUNTIME_ALLOW_WRITES=1` and
`LOGAN_RUNTIME_MODE` to one of the modes below. Never use a production database.

| Mode | Barrier and expected outcome |
| --- | --- |
| `http-drain` | GET port 8080 `/fault/slow`; wait for `HTTP_ENTERED`, Docker SIGTERM, release through port 8081 `/release`; full `drained-complete` response and container exit 0 |
| `native-drain`, `web-drain` | Wait for `PARSER_ENTERED`, Docker SIGTERM, release through port 8081 `/release`; actual worker commits one detail and container exit 0 |
| `native-block`, `web-block` | Wait for `PARSER_ENTERED`, inspect running lease and zero detail rows, Docker SIGKILL; container exit 137 |
| `native-recover`, `web-recover` | Start fresh container against same task after killed container; production lease expires, actual worker retries, emits corresponding `*_RECOVERED`, checks readiness and one detail, exits 0 |

Worker modes require `LOGAN_RUNTIME_MYSQL_DSN` for a dedicated `logan_it_`
database with the Java schema and both production migrations already applied,
and `LOGAN_RUNTIME_TASK_ID` for the prepared task. The harness does not create or
drop schemas. Seed native task with platform 1 and nonempty log_file_name; seed
web task with status 0 and content `["{\"t\":\"1\",\"c\":\"web%20recovered\",\"d\":\"1700000000000\"}"]`.

The native storage.Store fixture returns deterministic synthetic gzipped native
records and supports a controllable download barrier. It never accesses OSS.
The Web repository wrapper delegates all claim/publication operations to real
MySQL and pauses only the production parser callback after its claim. The
application, native/Web worker loops, parsing and publication are production
implementations. The HTTP route and control server are test fixtures.

A controller must retain Docker inspect/kill/wait evidence and SQL before/after
snapshots. Assert running fence 1 / attempts 1 / no details before KILL; done
fence 2 / attempts 2 / empty owner / one detail after restart. Retain publication
IDs and verify repeat delivery does not change them. The entrypoint alone does
not establish container signal delivery or stale-owner fencing; those require
controller assertions. Never report this fixture as real cloud OSS validation.
