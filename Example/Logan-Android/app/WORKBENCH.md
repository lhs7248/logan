# Actual Android workbench debug app

The debug variant has application ID `test.logan.dianping.com.logan.workbench`; the legacy release ID and example screen remain available. Its launcher is `test.logan.dianping.com.logan.WorkbenchActivity`. This activity uses the repository Java SDK, JNI and shared CLogan implementation. It requires no external storage permissions and has Android backup disabled.

Build with the existing Gradle 7.5 wrapper and AGP 7.4.2:

```sh
ANDROID_HOME="$HOME/Library/Android/sdk" ./gradlew -PworkbenchBuild -PworkbenchNdkVersion=27.0.12077973 :app:assembleDebug :app:testDebugUnitTest
```

`workbenchBuild` skips the legacy Bintray/Maven publishing plugins, which are unnecessary for local compilation. `workbenchNdkVersion` selects the installed NDK without changing the normal SDK project's requested version. The APK is `app/build/outputs/apk/debug/app-debug.apk` and contains the native libraries for all four existing ABIs.

Provision a private `files/workbench-profile.json` through `adb run-as` after installing this separate debug package. Do not put its values in command arguments, URLs, intents, source, screenshots or evidence. Its required fields are `receiverUrl`, `appId`, `uploadToken`, `nativeKeyBase64`, `nativeIVBase64`, `deviceId`, and `projectId`. `receiverUrl` must be exactly `http://127.0.0.1:8091/logan-web/api/workbench/v1/sdk/native`; use USB `adb reverse tcp:8091 tcp:8091`. Queries, credentials in URLs, remote hosts, and other paths are rejected. HTTP is allowed only for loopback in this debug variant.

The native key and IV must each decode to 16 printable ASCII bytes. Generate each using a cryptographically secure random selection from an ASCII alphabet, then Base64 encode. This local example constraint preserves the current Java SDK's byte-array-to-String and JNI UTF-8 handling; it is not a restriction on the Go native decoder protocol. Arbitrary binary key bytes would be changed by the legacy Java conversion and are rejected before initialization. Credentials and keys are never displayed. Missing/invalid profiles show an actionable message; provision/correct the file and force-stop/relaunch. Profile and log data remain in private app storage across relaunch.

The buttons have stable IDs `workbench_collect`, `workbench_upload`, and `workbench_legacy`. Status and capture text IDs are `workbench_status` and `workbench_capture`. A collection creates exactly three actual `Logan.w` calls followed by `Logan.f`, with messages:

```text
android-workbench-<fresh UUID> record=1 installation=<stable UUID>
android-workbench-<fresh UUID> record=2 installation=<stable UUID>
android-workbench-<fresh UUID> record=3 installation=<stable UUID>
```

`files/workbench-last-capture.json` persists the marker, installation identifier, actual log date, and exact messages. These records share the SDK's daily encrypted log file, so later captures on the same day append rather than delete existing logs. Upload calls the actual `Logan.s(url, date, headers, callback)` overload with package version/build read from PackageInfo. No upload ID is supplied, allowing receiver hash-based replay detection for identical bytes/metadata. Upload never silently collects more records.

`files/workbench-last-upload.json` persists sanitized callback status, marker, acceptance receipt/asset/run IDs and display text. HTTP success without a valid acceptance envelope is an error. Acceptance with an active/unknown parse run is explicitly distinct from a `succeeded` parse response. The app does not possess an administrative credential and does not poll the administrative parse API; inspect the returned run in the workbench for its later result. The legacy example remains reachable via its button and can append its original example logs to the same SDK stream.

Each upload snapshots its capture marker/date and a fresh submission ID before entering the SDK queue. A 30-second app-side callback timeout restores the buttons and records `outcome: timeout`; it does **not** cancel the SDK transport or prove that the server rejected the upload. The message directs the operator to check the workbench before retrying. This also recovers when the legacy SDK cannot find/copy its dated file and therefore never invokes the HTTP callback. Pending/timeout states have no fabricated HTTP status. The status file stores `submissionId`, the original `marker`, `fileDate`, and `outcome`; actual callbacks additionally carry `httpStatus`.

A late callback may update the same submission after its UI timeout. Creating another capture, starting another upload, or recreating the activity invalidates the earlier submission for UI/status-file updates, so it cannot overwrite newer state or attribute its receipt to a different marker. These guards do not cancel SDK work. Collection feedback says records were queued and flush requested; server parse/content verification is the evidence that the writes completed.
