#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
classes=$(mktemp -d "${TMPDIR:-/tmp}/logan-android-test.XXXXXX")
trap 'rm -rf "$classes"' EXIT
sdk=Example/Logan-Android/logan/src/main/java/com/dianping/logan
javac -d "$classes" tests/mobile/java/android/util/Log.java \
    tests/mobile/java/com/dianping/logan/*.java tests/mobile/java/AndroidUploadTest.java tests/mobile/java/AndroidUploadTlsTest.java \
    "$sdk/SendLogDefaultRunnable.java" "$sdk/SendLogCallback.java" \
    docs/examples/mobile/WorkbenchLoganUpload.java
java -cp "$classes" AndroidUploadTest
keytool -genkeypair -noprompt -alias upload-fixture -keyalg RSA -keysize 2048 \
    -validity 2 -dname CN=localhost -ext SAN=dns:localhost -storetype PKCS12 \
    -keystore "$classes/tls.p12" -storepass synthetic-test-password \
    -keypass synthetic-test-password >/dev/null 2>&1
java -cp "$classes" AndroidUploadTlsTest "$classes/tls.p12"
