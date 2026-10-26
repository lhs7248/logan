# Manual investigation V1 readiness implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the existing log reader and manual investigation service a reviewable enterprise-intranet, single-instance release candidate.

**Architecture:** Retain the existing workbench UI, API and bounded file store. Add opt-in manual-v1 startup checks, instance health endpoints and offline, checksum-verified backup/restore. Test real HTTP business operations in isolated storage before and after restart/restore.

**Tech Stack:** Go net/http, existing workbench persistence, Python standard library operations and acceptance helpers.

## Global Constraints

- Preserve existing navigation, interactions, capabilities and data. Do not edit unrelated uncommitted files.
- AI, Aliyun push and source mapping remain unconfigured; keep their existing entry points and explicit unavailable states.
- Deployment target: enterprise intranet, one process on local disk, small scale; no multi-instance or production MySQL claim.
- Test destructive/fault/restore operations only in isolated temporary directories. Never replace the live runtime.
- No commit or push in this task without a new user instruction. Keep actual secrets and logs out of tracked evidence.
- Baseline commit: a5754a38bd9711780dbb92b7d0c4801867b80490. Existing product-design.md remains the UI baseline.

### Task 1: Startup, health and recoverable operations

**Files:** cmd/workbench/*.go and internal/workbench health files under Logan/ServerGo; new script/workbench-ops.py and script/test_workbench_ops.py; focused deployment instructions under Logan/ServerGo/docs.
**Interfaces:** WORKBENCH_PROFILE=manual-v1 enables stricter startup validation without breaking default local clients. /healthz is liveness; /readyz returns 503 when closed or persistence unhealthy, without exposing private state. Offline operations accept explicit data/auth/backup/destination paths, refuse locked data, reject symlinks and existing output, and never print secrets.

- [x] Write and run focused failing tests for invalid auth grants, profile privacy/binding checks, unhealthy readiness, live-store backup refusal, checksums and non-destructive restore.
- [x] Implement the smallest compatible changes, keep default local startup supported. Validate project IDs/token uniqueness/grants against known projects; profile refuses world/group-readable auth and unsafe direct network exposure unless explicitly acknowledged for an intranet gateway.
- [x] Implement a versioned backup with state+auth+optional explicitly selected private configuration files, integrity manifest, private output, offline flock and restore into a new directory only. Document encryption/access-control responsibility, same-binary rollback and local-disk limits.
- [x] Run Go targeted tests and Python operations tests. Record exact commands/counts and review limitations.

### Task 2: Isolated acceptance and release gap matrix

**Files:** new script/verify-workbench-manual-v1.py, focused tests if needed; docs/product/2026-10-21-logan-workbench/manual-v1-readiness-2026-10-26.md and sanitized evidence JSON.
**Interfaces:** consumes built cmd/workbench binary and Task 1 backup/restore tool, runs only temporary synthetic projects/credentials and loopback ports. Reuse existing verify-workbench.py for upload, strict failure/reparse, filters, evidence, hypothesis, validation and immutable local ticket update.

- [x] Execute existing real HTTP manual workflow against an isolated runtime and verify viewer/project isolation separately.
- [x] Stop/restart the process; compare original bytes, parsed events and manual issue update records. Perform backup, restore into new paths and verify equivalent authenticated business reads.
- [x] Exercise exclusive lock, malformed state, interrupted process and resource limits using focused tests; record measured scale rather than inventing an SLA.
- [x] Run required full Go harness, existing workbench UI tests and affected Python tests; audit unrelated baseline hashes and live runtime read-only digests.
- [x] Publish passed/failed/not-run gap matrix. Actual gateway TLS, private backup destination and business load soak remain production-environment acceptance conditions when unavailable locally.

### Task 3: Independent final review

- [x] Review the scoped diff for spec compliance, security, compatibility and meaningful evidence; fix important findings and rerun covering tests.
- [x] Confirm user data and unrelated files retained, no real provider integrations introduced, and report concrete release conditions without claiming unverified production readiness.

## Execution result

Automatable scope completed; Go full, UI 75, Python 47 and isolated HTTP 22 checks passed. Independent review approved after alternate-project object-isolation check correction. Target-environment release conditions remain explicitly unverified in the readiness report. User authorized a scoped local commit on 2026-10-26; no remote push. Existing local runtime was backed up, rebuilt and restarted; existing state/config byte digests retained.
