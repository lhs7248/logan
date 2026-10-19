# Isolated Native reparse acceptance

The runner uses the real CLI, a real disposable MySQL 8.4.11 database, and an
unsigned local OSS-shaped transport. It never publishes a host port and requires
an explicit dedicated `logan` Unix socket and `--allow-isolated-runtime`.

Build the current CLI for the Linux engine architecture, then pre-load the chosen
Go runtime image and pull `mysql:8.4.11` and `python:3.12-alpine` on that engine.
Run `script/verify-native-reparse.py --help` for required arguments. Both evidence
and work directories must be new; the work directory and CLI binary must be
visible to the dedicated engine. Use paths under its explicitly shared temporary
directory. No real OSS credentials are needed or accepted by the runner.

The runner explicitly applies migrations 001–003 to a random
`logan_reparse_it_<random>` database. It seeds completed tasks with an explicit
Java-shaped first-member prefix, then reparses stored gzip containing later
members and duplicate records. This is **not** a claim that Java produced these
particular rows. The original Java source and behavior are unchanged.

Checks cover default dry-run full-state immutability, plan tampering, write guards,
append-only preservation, idempotent apply/rollback, same-operation and competing
operation concurrency, stale plans, divergent prefixes, changed objects, corrupt
CRC, record limits, forced second-insert transaction failure, and rollback refusal
after a manual mutation. Full business/audit table and object digest snapshots,
redacted command exits, and label-checked cleanup are saved. An audit row remains
after a successful rollback by design. Commit-ack interruption and production or
cloud OSS behavior are outside this runner's acceptance scope.

Additional real-MySQL cases reject an insert trigger that coerces content and a
70,000-byte TEXT value with `sql_mode=''`, checking that neither details nor audit
rows commit. A separate insert trigger sleeps at the precommit cutpoint; the runner
observes the server-side sleep, sends SIGKILL to the CLI container, verifies exit
137 and complete rollback, then retries the same plan exactly once. This proves
interruption before commit, not loss of the commit acknowledgement.

Controller paths must be absolute, fresh, separate directories outside the
checkout, without commas. The Docker executable and CLI binary must be existing
absolute executable files. SIGTERM enters the interrupt path; cleanup ignores
further INT/TERM while removing only resources whose ownership label matches.
Snapshots and summary use mode 0600; synthetic credential environment files are
removed on completion. Object mutations and digest snapshots run inside the OSS
container so shared-mount cache timing cannot weaken changed-object checks.

Follow-up controller `script/verify-reparse-confirmation.py` exercises real MySQL COMMIT OK withholding for both apply and rollback, independent committed-state witnesses, client exit137/no output and exact-operation retry. It then checks original Java/Tomcat and verified Go HTTP readers before/after repair/rollback, including deleted IDs. See `docs/reparse-confirmation.md` for scope and actual evidence; the earlier controller alone still does not claim this fault point.
