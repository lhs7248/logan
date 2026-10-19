# Real MySQL integration

Run from `ServerGo` with a disposable MySQL account permitted to CREATE/DROP DATABASE:

```sh
LOGAN_IT_MYSQL_DSN='<credentials>@tcp(127.0.0.1:3306)/logan_it_control?parseTime=true' \
LOGAN_IT_ALLOW_WRITES=1 \
go test -tags 'stdjson integration' -timeout 3m -count=1 ./tests/integration/mysql
```

Both variables are required; missing configuration fails the suite, including `-run '^$'`. Compile without running TestMain using `go test -tags 'stdjson integration' -c ./tests/integration/mysql`.

The named control database must exist and start with `logan_it_`. The suite only connects to that database to create a new cryptographically random `logan_it_<32 hex>` database. It applies the original four Java table definitions (verbatim from `Server/README`) and migrations 001/002 to the new database. It drops only the newly created database, never the supplied control database. No AutoMigrate is used. Interruption of the parent process may leave the random test database behind; inspect its identity manually before removing it.

Tests exercise Web publication/query/idempotency and legacy completed-page overwrite, Web retention, repository publication rollback, native retry, single-use upload intents, reference-safe retention, durable deletion state across connections, lease competition, and two separate OS processes: one is killed after a committed deletion claim; the replacement waits for actual database-clock lease expiry, reclaims and completes it. The old claim must fail after takeover. Recovery waits use bounded polling and explicit stdout barriers.

This suite does not contact OSS. It proves database outbox state, not external object deletion. Native parser recovery also kills a real child after its parse callback barrier, waits for the unmodified production 30-second lease to expire, and publishes through another process. It asserts running/done states, fence advancement, attempt count, exactly one detail, and stable detail IDs on repeated delivery. Web parser process termination is not covered. The child-process environment variables prefixed `LOGAN_MYSQL_CHILD_` are internal test coordination, not user configuration.
