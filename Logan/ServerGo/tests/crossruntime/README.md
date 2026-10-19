# Original Java → Go → original Java data rehearsal

This opt-in suite uses one **random disposable MySQL database** named
`logan_crossruntime_<random lowercase alphanumeric suffix>`. Never point it at a
production database. It does not run automatically in ordinary Go/Maven tests.

The Java phase compiles the unchanged `Server/src/main/java` through the existing
`tests/legacy-java/pom.xml`, loads the original four MyBatis mapper XML files, and
uses the real mapper implementations against MySQL. The final Java phase also
queries through the original native/web task/detail services and content handlers.
There are no Java repository or service simulators in this suite.

The Go phase uses the production metadata/query repositories, `native.QueryService`
and `weblog.Service`, including background workers and transactional publication.
It reads Java completed detail IDs and payloads, processes Java pending native/web
tasks, then creates and analyzes Go native/web tasks. Java starts again and checks
preserved Java IDs/payloads, Go-generated IDs, task statuses, payloads, type/keyword
queries and detail ID queries in the same database.

Native object bytes are **explicit local gzip fixtures** through a fixture reader.
This proves stored gzip and data/service compatibility; it does not prove a real OSS
request, encrypted SDK upload, HTTP deployment, production cutover or SDK integration.
Web fixtures include Chinese, literal plus and percent characters. The original web
tables use three-byte `utf8`, so emoji is tested only in native `utf8mb4` data; the
suite does not change the original schema to admit unsupported web characters.

## Prepare

1. Create a fresh random database on isolated MySQL. Apply
   `../integration/mysql/java_schema.sql`, then the two files in `../../migrations`
   in order. The Java seed refuses occupied original tables.
2. Compile Java with `mvn -Dtest=CrossRuntimeDatabaseTest test` from
   `../legacy-java`. Without `CROSSRUNTIME_PHASE`, the integration test is skipped.
3. Build a portable JRE/JUnit bundle with
   `python3 build-java-bundle.py --output /tmp/<new-bundle-directory>`.
   The directory must not already exist. It contains resolved dependencies,
   compiled original classes, test classes and byte-for-byte original XML with
   SHA-256 provenance. Run its `run.sh` under JRE 17 in the disposable container.
4. Compile a Go test binary from the ServerGo directory:
   `GOOS=linux GOARCH=arm64 go test -c -tags=crossruntime -o /tmp/crossruntime.test ./tests/crossruntime`.
   Choose the architecture matching the container.

## Run in sequence

All three processes share a writable fixture/evidence directory. Java and Go are
run sequentially and exit before the next phase starts.

Java seed environment:

```text
CROSSRUNTIME_DISPOSABLE=YES
CROSSRUNTIME_PHASE=seed
CROSSRUNTIME_JDBC_URL=jdbc:mysql://mysql:3306/logan_crossruntime_<random>?useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai
CROSSRUNTIME_DB_USER=<disposable user>
CROSSRUNTIME_DB_PASSWORD=<disposable password>
CROSSRUNTIME_DIR=/evidence
```

Run the Java bundle's `run.sh`. It writes `java-seed.json` and
`java-pending.gz`. When using Maven directly, execute from `tests/legacy-java`;
`CROSSRUNTIME_MAPPER_DIR` may explicitly point to copied original mapper XML.

For Go, supply the same `CROSSRUNTIME_DISPOSABLE` and `CROSSRUNTIME_DIR`, plus
`CROSSRUNTIME_DSN` with `parseTime=true&charset=utf8mb4&loc=Asia%2FShanghai`.
Execute `crossruntime.test -test.v -test.run TestOriginalJavaGoJavaDataRoundTrip`.
It writes `go-produced.json` with fixed expectations and generated IDs.

Finally run the Java bundle again with `CROSSRUNTIME_PHASE=verify` and identical
connection/evidence settings. Success writes `java-recheck.json`. Preserve process
exit codes and logs for **each phase**; compilation or an opt-in skip alone is not
a successful round trip. Destroy the disposable containers/database after evidence
collection. Do not persist DSNs/passwords in versioned evidence.

## Optional original HTTP deployment

`build_java_war.py --output /tmp/<new-name>.war` packages the original compiled
classes, original webapp/Spring context and resources for Tomcat 9. It uses the
existing baseline POM's dependency graph, including its compatible Lombok compiler,
not the original old Maven build. It overrides **only the staged copy** of
`db.properties` and `oss.properties`; original Server files remain unchanged.
The script requires the disposable acknowledgement/JDBC/database credentials above.
OSS config always uses explicit synthetic fixture credentials and defaults to an
unreachable local endpoint; an optional `CROSSRUNTIME_OSS_EMULATOR_ENDPOINT` must
be described as an emulator, never real OSS. The original scheduler remains in the
WAR; its staged retention is 36,500 days for this disposable test.

Building a WAR is not evidence it started or served a request. Record Tomcat startup
and actual HTTP responses separately if this optional deployment is executed.
