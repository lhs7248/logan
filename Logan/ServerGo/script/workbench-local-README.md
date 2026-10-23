# Persistent local workbench

Run from the repository root with Python 3.9+ and Go installed:

```sh
python3 Logan/ServerGo/script/workbench-local.py init
python3 Logan/ServerGo/script/workbench-local.py build
python3 Logan/ServerGo/script/workbench-local.py start
python3 Logan/ServerGo/script/workbench-local.py status
```

The UI is at `http://127.0.0.1:8091/workbench/`. The service only binds loopback. `start` installs a current-user LaunchAgent under `~/Library/LaunchAgents` and waits for its owned process, matching binary hash, authenticated `local-mobile` project session and workspace. An unrelated process listening on 8091 causes failure and is never killed. `start` is idempotent. launchd restarts the service after failure and at user login independently of a terminal or chat. macOS must permit launchd to read the checkout; Desktop privacy restrictions can require relocating the runtime/launcher to an accessible location.

```sh
python3 Logan/ServerGo/script/workbench-local.py restart
python3 Logan/ServerGo/script/workbench-local.py stop
```

`stop` unloads only the exact owned LaunchAgent after process verification, then waits up to 12 seconds for the originally verified process to exit. It treats a reused PID with a different executable as the original process having exited and never signals that unrelated process. The port probe allows TCP TIME_WAIT rebinding, matching Go listener behavior. It retains the plist, binary, credentials, keys, logs and business records; the retained RunAtLoad plist enables startup at the next user login. To rebuild, stop, build and start. `run` runs the binary in the foreground with the same explicit local environment; use it for diagnosis only while the LaunchAgent is stopped. `status` recognizes the persistent LaunchAgent, so a manually launched foreground service is reported as an unverified listener.

The default private runtime is repository `.data/workbench-local` (gitignored), with directory mode 0700 and private JSON mode 0600. `--root /absolute/private/path` before the command selects an isolated persistent runtime; each root has its own LaunchAgent identity but every instance uses port8091, so only one can run. `init` never rotates or overwrites existing credentials or keys. Incomplete/inconsistent existing configuration fails for operator inspection. Do not remove a runtime to repair a startup problem.

- `settings.json`: random separate admin/upload tokens, random 16-byte ASCII native key/IV, project/application scope and persistent test installation ID.
- `auth.json`: project, operator admin grant with raw/export permissions, Android application-scoped upload client.
- `data/`: server business state and original encrypted assets.
- `bin/workbench`, `build.json`: persistent executable and build hash.
- `run.json`: launched process ID, executable path and hash for ownership verification.
- `logs/service.log`: private stdout/stderr from launchd. Do not include its contents in shared evidence without review.

The local Android test key and IV each use 16 independently cryptographically random ASCII letters, digits, `_` or `-` (96 bits of entropy per value), then base64 encoding for the shared profile. The legacy Java/JNI SDK converts key bytes through a String and UTF-8, so arbitrary binary keys would change in transit. This restriction belongs to this local Android profile; the Go native decoder still supports its existing protocol. Existing incompatible local profiles fail clearly and retain all keys rather than being silently regenerated.

The service receives a small explicit environment. Source, model, MySQL, legacy retrieval and Aliyun push configuration from the operator's shell are not inherited. No model/source/push configuration or production data is initialized.

For operator login, `credentials --show` explicitly prints the local administrator token with URL/project. Never record this output in screenshots, reports, chat, or shared command logs. The default `credentials` command prints no secrets.

Export the Android profile directly to a new private file, without printing credentials:

```sh
python3 Logan/ServerGo/script/workbench-local.py app-profile --output /absolute/private/path/workbench-profile.json
```

The destination must not exist and its parent must already exist. The resulting mode0600 JSON contains `receiverUrl`, `appId`, `uploadToken`, `nativeKeyBase64`, `nativeIVBase64`, `deviceId`, `projectId`. It never contains the administrator token. `app-profile` alone reveals no secrets; `app-profile --show` explicitly prints the sensitive profile for operator use.

Provision this file into `files/workbench-profile.json` under the separate debug application `test.logan.dianping.com.logan.workbench` using Android `run-as` and private input redirection; never place it in APK resources, an intent, a public device directory or a URL. Use USB reverse `tcp:8091 tcp:8091` for the receiver. Keep the private file and installed test App for repeated sessions so the installation ID and credentials remain stable. This helper does not install or modify any Android App.

Run the focused standard-library tests with:

```sh
python3 -m unittest discover -s Logan/ServerGo/script -p test_workbench_local.py
```

These tests cover private initialization/reuse, capability separation, explicit profile export, environment scope, launch policy, redirect rejection, port collision and ownership protection. Actual launchd login persistence, service restart and Android end-to-end verification require separate host/device integration evidence.

## Repeatable physical Android check

After installing the separate debug App, run from the repository root:

```sh
python3 Logan/ServerGo/script/workbench-android.py --serial YOUR_DEVICE_SERIAL provision
python3 Logan/ServerGo/script/workbench-android.py --serial YOUR_DEVICE_SERIAL verify --report /tmp/android-check.json
```

Provisioning preserves an identical private App profile and refuses to overwrite a different one. It restores the USB loopback forwarding, which can be lost when reconnecting the phone. Relaunch the App after first provisioning, create and upload logs using its buttons, then run `verify`. Verification reads the actual App capture/receipt, compares all three messages with the parsed run, and compares the App retained encrypted daily file with the server asset and raw download. It does not synthesize or upload logs. Use `--baseline /tmp/android-check.json` after a service/App restart to compare the same capture. The profile is not printed or included in reports.
