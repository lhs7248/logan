# Manual investigation V1: single-instance operations

Scope: one process on local POSIX disk, enterprise intranet, small scale. This profile does not configure AI, Aliyun push or source mapping. Existing UI and local startup remain available.

## Startup and health

Set `WORKBENCH_PROFILE=manual-v1`, `WORKBENCH_AUTH_FILE` to a private regular file (0600), `WORKBENCH_DATA_DIR` to an absolute, canonical local private directory owned by the process user (0700), and `WORKBENCH_ADDR=127.0.0.1:8091`. Auth requires nonempty unique project IDs, unique tokens of at least 16 characters, actors and grants to known projects with viewer/investigator/reviewer/admin roles. SDK configuration retains its existing validation.

Serve through an enterprise gateway with TLS, access control, request limits and network restrictions. A direct non-loopback bind requires the explicit operator acknowledgement `WORKBENCH_INTRANET_GATEWAY_ACK=true`; this acknowledgement does not implement a gateway or TLS. If the data directory does not exist, its immediate parent must already be private and owned by the process user; startup creates only the configured directory. Auth and data paths reject symlink components. Profile rejects optional MySQL storage. The default unset profile preserves existing local startup behavior.

Unauthenticated `GET /healthz` reports process liveness. `GET /readyz` returns 503 after close or a detected persistence failure; both return generic text with no project, token or private error details and disable caching. Readiness is instance state, not a disk capacity probe or business SLA.

## Offline backup and restore

Stop the instance and use the same binary/version when restoring or rolling back. These commands refuse a held store lock, symlinks, invalid JSON/state/auth, checksum mismatch and existing output. Use canonical paths (on macOS `/tmp` is a symlink; resolve it before use). Never point rehearsal destinations at the live data directory.

From ServerGo, using operator-selected paths:

```sh
python3 script/workbench-ops.py backup --data-dir "$DATA" --auth-file "$AUTH" --output "$NEW_BACKUP" --config-file "$PRIVATE_CONFIG"
python3 script/workbench-ops.py verify --backup "$NEW_BACKUP"
python3 script/workbench-ops.py restore --backup "$NEW_BACKUP" --destination "$NEW_RESTORE"
```

`--config-file` is optional and repeatable; basenames must be unique. Explicitly retain files holding native key/IV settings, Web private keys, legacy configuration and other required environment settings. Environment variables are not captured automatically. Auth and all business state (including encoded original bytes and investigation records) are included. A state.json must already exist; a never-used empty directory is refused.

Backup contains `manifest.json` (format version 1, SHA-256 per payload), `data/state.json`, `auth.json` and optional `config/<basename>`. Restore validates everything before creating a new directory, then writes `data/state.json`, `auth.json` and optional `config/` files; it does not overwrite or switch any runtime. Set restored `WORKBENCH_DATA_DIR` and `WORKBENCH_AUTH_FILE` explicitly and repoint any retained key/config file references before starting. Verify authenticated business reads before routing traffic. `.lock` and temporary writes are not restored.

Outputs use directory mode 0700 and file mode 0600. Backups contain credentials and user logs in plaintext: operators must provide private storage, encryption at rest/transport, controlled access, retention and deletion. Checksums detect accidental corruption, not malicious replacement of both manifest and payload. Keep the binary/version and independently protected backup metadata. The tool holds the runtime flock throughout snapshot copying; do not edit auth/config or backup contents concurrently. Use a trusted private parent directory; it is not hardened against a hostile local user racing filesystem mutations. A process or power interruption during output creation can leave an incomplete new directory; verify it, retain the old runtime, and retry with another new path. Files and created directory entries are synced before success; filesystem/hardware durability remains an operator responsibility. Atomic multi-file snapshotting of externally edited configuration is not claimed.

No multi-instance, network filesystem, production MySQL, actual gateway/TLS or sustained business-load acceptance is established by local tests.
