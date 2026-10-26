# Internal push callback V1 implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement tasks with isolated briefs and independent review.

**Goal:** Execute the user-approved provisional internal-service push protocol and reflect durable callback progress without fake retrieval success.
**Architecture:** Add an optional adapter and independent callback route around existing workbench persistence; retain draft-first UI and unconfigured fallback. Test on loopback service doubles, leave live runtime and other uncommitted work unchanged.
**Tech Stack:** Go net/http/HMAC, existing file store and JS modules/tests, Python isolated HTTP acceptance.

## Global Constraints

- Spec: ../specs/2026-10-26-internal-push-callback-design.md. User explicitly authorized defining a replaceable provisional protocol; no real service contract exists yet.
- No real Aliyun/provider, AI/source mapping work; no invented client acknowledgement or log receipt.
- Preserve existing data/UI layout and all unrelated uncommitted changes including deployment docs and mobile schema/contracts.
- No commit/push or live runtime restart in this task. All integration tests isolated.

### Task1: Backend domain, adapter and callback

Own internal/workbench push*.go and focused tests, connectors/push*.go, minimal server.go/routes.go wiring, cmd/workbench main.go/runtime config tests. Read spec fully; expose exact API/workspace/status field names specified there to frontend owner. Use tests first, capture red, implement and run focused Go+race checks. Persist before send, no external lock, authenticated durable callback idempotency, nonregression and startup interruption. Add operator doc docs/internal-push-v1.md with exact protocol/signing and private config setup. Report /private/tmp/logan-push-v1-backend-report.md. No UI/Python helper writes.

- [x] Focused tests written, fail for missing behavior.
- [x] Implement domain/adapter/config without touching existing data.
- [x] Tests pass, report exact counts/evidence/limits.

### Task2: Existing UI integration

Own workbenchui/static/collection.mjs, minimal app.js poll, focused .test.mjs. Keep unconfigured behavior and existing forms/data. Add collections/attempt/event display, real send/retry/cancel with stable keys/confirmed reason, 2-second polling only for active collection states, project isolation. Write meaningful tests first, run complete Web suite.

- [x] Capability and status helpers tested, old drafts preserved.
- [x] Existing wizard/tracking views integrated with actual persisted server state.
- [x] Web suite passes.

### Task3: Real HTTP acceptance, self/independent review

Own new script/verify-workbench-push-v1.py and sanitized product acceptance under existing dated product folder. Exercise actual workbench binary against loopback internal-service double, signed success/failure/duplicate/old callback and service restart. Also run old manual V1 isolated acceptance, full Go harness and affected Python tests. Review scoped diff independently, fix findings, verify excluded file digests, report exact boundaries. No real-provider completion claim.

- [x] HTTP integration and old manual flow pass.
- [x] Full regression, independent review and fixes complete.
- [x] User-facing protocol and acceptance evidence saved, limitations explicit.

## Result

Platform increment completed and independently reviewed after fixing the uncertain wizard payload P2. HTTP push12, original manual22, Web85, existing Python47 and Go full passed; final UI embed/build rerun passed. Browser main flow uses only isolated protocol double; no actual provider/client receipt/upload correlation claimed. Existing 8091 runtime and prior uncommitted files retained; no commit/push. An isolated fixture was upgraded at the same URL with three private config/state files byte-preserved for user inspection.
