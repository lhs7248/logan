# Actual SDK fixture producers

These drivers execute the repository SDK sources without modifying them. The current CLogan includes the flush boundary repair described below. They write only to an explicitly selected fresh output directory. The marker strings are synthetic and contain Chinese, an emoji, a plus sign, and a percent sign.

- Native: compile the repository's shared CLogan C SDK and bundled mbedTLS AES implementation with a host C compiler, call `clogan_init/open/write/flush`, and retain its encrypted `native.bin`. The native SDK emits its own internal header record in addition to the marker record.
- Web: transpile the repository's TypeScript at load time, call its public `logWithEncryption` API, store through its actual persistence code with `fake-indexeddb`, and call its actual `report` API. Capture the upload JSON at `XMLHttpRequest.send`. This runs the whole SDK application path under Node with an IndexedDB emulator; it does **not** prove browser-native IndexedDB, browser networking, Android, or iOS wrapper behavior.

Prerequisites: Python 3, Node/npm, a C compiler, system zlib, OpenSSL. Install isolated test dependencies (network required for this explicit setup command):

```sh
npm install --prefix /tmp/logan-sdk-deps --ignore-scripts --registry https://registry.npmjs.org typescript@3.9.10 crypto-js@3.1.9-1 fake-indexeddb@2.1.1 idb-managed@1.0.9 es6-promise@4.2.8
python3 tests/sdk/generate.py --out /tmp/logan-sdk-fixtures --node-modules /tmp/logan-sdk-deps/node_modules
python3 tests/sdk/verify.py /tmp/logan-sdk-fixtures
```

Run from ServerGo. The generator intentionally refuses to reuse an output containing `native.bin`, because CLogan appends. No dependencies are installed automatically and no SDK source is patched. Artifacts:

| File | Use |
| --- | --- |
| `native.bin` | Actual CLogan encrypted upload body |
| `web-upload.json` | Actual WebSDK report request JSON |
| `web-expected.json` | Stable semantic marker, type, and SDK report result |
| `manifest.json` | Producer/environment limitations, source and output SHA-256 fingerprints, expected native marker |
| `test-rsa-private.pem` / `test-rsa-public.pem` | Ephemeral synthetic RSA material generated in output directory only |

Configure the integration servers' native AES key and IV as `0123456789012345` (both configurable with `--native-key` / `--native-iv`), and Web RSA private key from the generated temporary output. To use an existing **test** RSA pair instead, pass `--public-key path/to/public.pem` and use `verify.py --private-key path/to/private.pem`. Do not check real credentials or generated private keys into the repository.

`verify.py` independently decrypts both outputs with OpenSSL and inflates native gzip records, then asserts marker, log type, and native timestamp. This catches malformed producer fixtures but is **not** the Java/Go server parity test. HTTP integration must send these bodies to both actual servers and recover the markers from their persisted/queryable detail records. Ciphertext changes with runtime randomness; semantic markers remain stable.

Android and iOS platform wrappers/device sessions remain unexecuted by this harness.

For comparison against the untouched Java reference, add `--java-reference-rsa`. This derives the public key from already-public reference material embedded in `WebLogDecryptHelper.java`, keeping the extracted reference private key only in the temporary output directory. The verifier also writes `native-plain.bin` and `web-plain.json` (an array of raw decrypted Web log strings) for exact downstream assertions.

Run the opt-in test against production Go decoders:

```sh
LOGAN_SDK_FIXTURE_DIR=/tmp/logan-sdk-fixtures go test ./tests/sdk -v
```

Without that environment variable the test explicitly skips. A generated fixture whose public key is supplied externally must have the matching test private key placed at `test-rsa-private.pem` in its output directory for this test.

Observed with original reference RSA fixtures: production Go native decompression stopped after the SDK internal header and did not recover the marker; Go 1.27 also rejected the reference's 1023-bit RSA key. These are recorded failures, not SDK integration passes. Independent OpenSSL verification recovered both markers. Tests intentionally preserve semantic marker assertions.

The opt-in original Java counterpart is `tests/legacy-java/src/test/java/com/meituan/logan/baseline/SDKProductionCompatibilityTest.java`. From `tests/legacy-java`, run:

```sh
LOGAN_SDK_FIXTURE_DIR=/tmp/logan-sdk-fixtures mvn -Dtest=SDKProductionCompatibilityTest test
```

With original Java reference keys and actual SDK fixtures, this executed two independent cases: native marker recovery **failed**, encrypted Web marker recovery **passed**. The original Java runtime's BC provider fallback accepts its legacy private-key encoding without test provider reordering. The native failure therefore reproduces on both unchanged Java and Go. Without the environment variable both JUnit cases explicitly skip.

To test the Web live chain against legacy MySQL `utf8` (three-byte) columns, preserve the default full-Unicode fixture as a negative case and generate an additional BMP-only input through the same unchanged SDK:

```sh
python3 tests/sdk/generate.py --out /tmp/logan-sdk-java-bmp --node-modules /tmp/logan-sdk-deps/node_modules --java-reference-rsa --web-marker 'SDK_CHAIN_WEB_BMP_20261016 中文 + percent%'
python3 tests/sdk/verify.py /tmp/logan-sdk-java-bmp
```

`--web-marker` changes only the string passed to the public SDK logger. The default emoji case remains available to expose the original database character-set limitation. Manifest marker assertions follow the chosen input.

Update 2026-10-16: the Go native decoder now recovers both records, with a permanent actual CLogan regression fixture. Earlier failures above remain historical evidence. Native local upload/parse/query tests pass; see ../../docs/native-sdk-recovery.md for declared Java differences. The legacy Web RSA requirement remains. No real-cloud/device claim is added.
# CLogan flush boundary regression

`go test -tags stdjson ./tests/sdk -run TestNativeSDKFlushHasNoEmptyFrame -count=1`
compiles the current shared CLogan sources with `cc`, then checks complete frame
boundaries, strict gzip decoding, ordered record counts, repeated flush and
append behavior. It skips explicitly if no C compiler is available.

Set `LOGAN_SDK_LARGE_REGRESSION=true` to also generate and validate 32 MiB of
synthetic record content. This test uses the public C APIs; actual iOS wrapper,
HTTP, MySQL and browser evidence is recorded in
[the simulator repair report](../../docs/ios-empty-frame-fix.md).
