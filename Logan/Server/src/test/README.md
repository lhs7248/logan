# Web task mapper regression

`WebLogTaskMapperTest` loads the production MyBatis XML and queries real MySQL.
It creates a session-local temporary `web_task` table; it never deletes or inserts
rows in the persistent application table. The test checks both an empty table
(max ID 0) and a populated table (max ID 17).

Run with Java 8 and the project's Maven toolchain, supplying the test database
connection via environment variables (do not commit real credentials):

```sh
# Set LOGAN_TEST_JDBC_URL, LOGAN_TEST_DB_USER, LOGAN_TEST_DB_PASSWORD locally.
mvn -B -Dtest=WebLogTaskMapperTest test
```

Without `LOGAN_TEST_JDBC_URL`, the integration test is skipped, not passed.
The database user requires CREATE TEMPORARY TABLES permission.

On 2026-10-27 the test reproduced the original MyBatis BindingException before
changing SQL, then passed against the local MySQL 8 container after the change.
JUnitCore was used with the compiled test and cached dependencies because the
container could not resolve the Maven repository to fetch the Surefire provider.
WAR packaging passed separately with tests skipped; it does not replace the
explicit successful regression execution.

Empty-table fix review: only empty-table MAX(id) handling changed; populated-table
results and HTTP response shape are unchanged. Java controller baseline completed
with 13 tests, 0 failures/errors, 3 skipped. For this fix, the source lock updates the
reviewed WebLogTaskMapper.xml hash; the first-open service change is documented below.
No golden responses or controller hashes changed.

## First-open detail regression

WebLogDetailServiceImplTest uses a deliberately delayed queue to reproduce the
old return-before-write behavior. Before the fix, synchronous completion and
failure propagation tests failed; afterward all three tests passed: 201 entries
are written in batches of 200 and 1 before commit/return, a second-batch failure
propagates and triggers rollback, and empty input performs no writes.

The implementation uses TransactionTemplate explicitly because the current XML
configuration defines a transaction manager without enabling annotation-driven
transactions. Only Web batch parsing changes; Native queue behavior is unchanged.
This fixes first-read visibility, not concurrent parsing deduplication across
multiple requests or processes.

The source lock change for WebLogDetailServiceImpl.java is intentional: batch
writes now finish before the caller queries details, and failures are no longer
reported as successful enqueueing. No controller contract or golden changed.
Controller baseline: 13 tests, 0 failures/errors, 3 skipped. JUnitCore: all 4 tests
(including the real MySQL max-ID regression) passed. Packaging passed separately.

Live validation on 2026-10-27: uploaded three synthetic entries via the real HTTP
upload endpoint using device codex-first-open-20261027. Database state before the
first browser navigation was status=0 and detail count=0. A single list-to-detail
click rendered all three entries and the single-entry card, without refresh or a
prior detail API call. Database state afterward was status=1 and detail count=3.
