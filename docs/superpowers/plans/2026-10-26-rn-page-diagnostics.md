# RN Page Diagnostics Implementation Plan

> **For agentic workers:** Use subagent-driven-development with disjoint file ownership, tests first, root integration and independent review. User has explicitly authorized implementation and simulator-based HermesDemo pilot; no repeated approval gate. No commits/push unless requested.

**Goal:** Actual HermesDemo MiniApp opening evidence → native encrypted upload → Go normalized events → existing Web investigation, with process-restart retention.

**Architecture:** Versioned `xlb.page-diagnostic` content object over existing native protocol. Isolated Codegen diagnostic capability and private native collector in HermesDemo; independent application-scoped SDK client alongside existing project/keys/assets. Add normalization after native strict decoding. Web attempt grouping remains within existing reader.

**Tech Stack:** RN 0.85.3, Codegen, repository Logan native SDK (Objective-C/C on iOS; preserved Kotlin/Java/JNI Android integration), Go, existing Web UI and Node tests.

## Global Constraints

- Spec: ../specs/2026-10-26-rn-page-diagnostics.md; exact enums/identity/time/privacy contract is shared authority.
- Do not modify hxl-host-app or host-app-xlb; preserve their dirty state and existing real-device assets.
- User confirmed 2026-10-26: switch acceptance to HermesDemo iOS simulator Debug; retain Android implementation without claiming Android runtime acceptance. Aliyun/source deferred.
- Do not change unrelated HermesDemo untracked docx, signing/application identity, existing MF/TurboModule behavior.
- Fault injection clearly labeled; logging failures isolated; no raw error text, request body, account identity or credentials in versioned evidence.

## Tasks

- [x] Native/RN integration (HermesDemo owner): tests first for attempt lifecycle and validation; implement standalone Codegen module + private persistent collector linking existing native AAR; actual App upload + receipt; instrument real MiniApp lazy loader, error boundary, layout/ready and retry; add bounded Debug test scenarios and preserve original UI. Build/test and give root exact install/launch commands. Do not mutate device or Go runtime.
- [x] Go normalization (backend owner): new package-local normalization helper/test after protocol decode; namespace/version validation, original/envelope time retention, allowlisted fields, clear malformed diagnostics, compatibility tests. Do not touch runtime config/UI.
- [x] Web association (UI owner): pure page-attempt grouping helper/tests; integrate filter/summary in existing reader without removing current operations; evidence links, incomplete/abandoned/slow semantics, injected label. Do not touch Go parser or native repo.
- [x] Root environment and app profile: establish clean dirty-state baselines; install dependencies/emulator support from official tools without accepting new terms automatically; append idempotent private Hermes upload client preserving settings/auth/key/example/business clients. No real payload reports.
- [x] Integration acceptance: run normal/error/blank/slow/retry on emulator through real App buttons; exact snapshot/ciphertext/event identity checks; Go full/profile and relevant Web/native/RN checks; restart persistence and old assets retained; read actual Web UI and capture safe screenshots. Review/fix defects; preserve reports with honest coverage limits.

## Representative checks

Go tests must assert `normalizePageDiagnostic` extracts only the schema namespace, preserves old event record fields for legacy content and uses occurredAtMs instead of replay envelope time. Web tests feed a started→slow→ready attempt and require success-with-slow, not failure; abandoned and missing-ready remain distinct. RN tests exercise real state tracker with injected clock/sink, retry isolation and suppressed sensitive values. Native tests reject arbitrary JSON keys/IDs, preserve damaged journals and receipts, and enforce bounds without deleting old data.

Root checks all prior asset hashes and private config hashes before/after runtime changes. Actual generated event content may only contain contract fields. Each upload proof binds installed package, attempt receipt, actual SDK ciphertext snapshot and strict Go run; emulator tests are distinct from synthetic unit cases.

## Approved iOS continuation (2026-10-26)

- [x] Bind the existing iOS Codegen bridge to the repository Objective-C/C Logan SDK; preserve the current RN lifecycle contract and existing native features.
- [x] Use private durable config, validated journal, immutable ciphertext snapshots and upload receipts; verify process restart preservation without clearing data.
- [x] Run the real MiniApp in a dedicated iOS simulator: normal, loader failure, render failure, suspected blank, slow/recovery, leave/retry and actual remote unavailable. Label controlled injections.
- [x] Verify exact SDK bytes and original events against strict Go parse, exercise the existing Web investigation UI, restart App/service, compare prior assets/configuration and keep a sanitized acceptance report.

## 验收完成

见 [iOS 实际验收报告](../../product/2026-10-21-logan-workbench/hermes-ios-rn-page-acceptance-2026-10-26.md)。Android 实现保留，实际运行验收范围为用户确认的 iOS 模拟器；模型、阿里云与源码映射未验收。
