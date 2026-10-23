# Persistent local workbench and actual Android App chain

> Execute within the existing approved design and user authorization. Use scoped subagent implementation with independent review; preserve the dirty SDK integration work and all user data. No source mapping, model/provider setup or Aliyun work.

**Goal:** A persistent, repeatable localhost workbench service and real Android test App records → native SDK upload → Go parse → Web read verification on the attached device.

**Architecture:** Keep the independent Go workbench/UI and native endpoint. Add a local operator CLI storing credentials, native decoder key/IV, binary, logs and business state under gitignored `.data/workbench-local/`, never `/tmp`. A user LaunchAgent runs the service on loopback8091 with login/restart persistence. USB reverse makes that receiver reachable from a separately installed debug example App without LAN exposure. Runtime App profile lives in its private files directory, not the APK, URL, intent, screenshot or console.

**Constraints:** No user-data deletion or replacement of installed business Apps. New example debug application ID `test.logan.dianping.com.logan.workbench`. Default project `local-mobile`; application-scoped upload client for that package. Do not expose admin token to App; source/model/push stay disabled. Use actual repository Java/JNI/CLogan, no file lookup/scheduler/transport stubs in device acceptance. UI existing layout preserved.

## Shared profile contract

`app-profile` export/provision JSON has `receiverUrl`, `appId`, `uploadToken`, `nativeKeyBase64`, `nativeIVBase64`, `deviceId`, `projectId`. Runtime file is `files/workbench-profile.json` under new test App. App reads the profile before SDK initialization; expected receiverURL is loopback8091 `/logan-web/api/workbench/v1/sdk/native`. Native key/IV are new random16-byte local test keys matched to this service. Credentials/config file0600, directory0700, excluded from Git. No credentials in output unless explicit credentials command (operator use only); credentials commands must avoid including output in evidence.

## Tasks

- [x] Operator runtime: create `Logan/ServerGo/script/workbench-local.py` and focused stdlib tests. Commands init/build/start/stop/restart/status/run/credentials/app-profile. Init must not overwrite existing runtime state; support explicit root argument for isolated tests. Start is idempotent, validates service identity / selected project readiness and port ownership, not any process's HTTP200; stop targets owned process only. LaunchAgent installation idempotent, autorestart independent of chat PTY, project and paths stable. Use project-local private configuration and preserve across service restart. Keep original services/routes intact.
- [x] Android example: add a clear debug-only workbench test activity/launcher and application profile initialization. Distinct debug package, private files, no token in APK or logs. Buttons create three unique identifiable records with actual Logan.w, flush and upload using actual Logan.s custom headers. Show real accepted receipt/asset/run and error; distinguish acceptance from parse success. Persist installation marker and profile across restart. Existing legacy example actions remain reachable. Build repository actual native code using installed toolchain; do not weaken encryption/protocol to pass.
- [x] Integration: initialize stable local runtime and install/start user service, provision private App profile via adb run-as, USB reverse, build/install separate debug package and use actual App actions to generate/upload. Compare returned asset/run to persisted Go parse, exact App messages and original encrypted bytes; inspect Web same asset and parse states. Verify duplicate/failure feedback appropriately, App configuration survives relaunch and service config + business records survive restart. Browser proof and actual device UI proof.
- [x] Independent review & regression: review runtime ownership/secrets/LaunchAgent lifecycle, App real SDK initialization/network callbacks and data retention. Required Go full/frontend/prototype as affected, runtime tests, actual App build, device action evidence. Report physical Android coverage precisely; no claim iOS physical, source, Aliyun or production readiness.

## Acceptance evidence

Document reproducible startup and App procedure plus sanitized exact object IDs, hashes/content counts, service restart and device run results. Retain all generated live state and configs in persistent ignored directory; version only templates/scripts/test source and redacted reports. User can open real workbench8091 without regenerating token/config each turn.

Actual outcome and limits: [physical Android acceptance](../../product/2026-10-21-logan-workbench/physical-android-acceptance-2026-10-23.md). Device native file and server raw bytes match, 3 exact marker events + native header, idempotent replay, service restart, App relaunch and real disconnected/recovered upload verified.
