package workbench

import (
	"encoding/json"
	"logan/servergo/internal/workbench/connectors"
	"strconv"
	"strings"
	"time"
)

func (q *request) route() (any, int, *apiError) {
	path := strings.TrimPrefix(q.r.URL.Path, Base)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	method := q.r.Method
	b, e := q.input()
	if strings.HasPrefix(q.r.Header.Get("Content-Type"), "multipart/") {
		b = Record{}
		e = nil
	}
	if e != nil {
		return nil, 0, e
	}
	if method == "GET" {
		switch path {
		case "/legacy/tasks":
			if q.s.cfg.Legacy == nil {
				return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Read-only legacy adapter is not configured")
			}
			var tasks []connectors.LegacyTask
			if e := q.external(func() error { var err error; tasks, err = q.s.cfg.Legacy.List(q.r.Context(), q.project); return err }); e != nil {
				return nil, 0, e
			}
			return tasks, 200, nil
		case "/operations":
			key := strings.ToLower(q.r.URL.Query().Get("key"))
			operation := q.r.URL.Query().Get("operation")
			for _, op := range q.list("operations") {
				if op["actor"] == q.actor.Actor && op["key"] == key && (operation == "" || op["operation"] == operation || strings.HasSuffix(str(op["operation"]), operation)) {
					return op, 200, nil
				}
			}
			return nil, 0, fail(404, "NOT_FOUND", "No operation found for this actor/project/key")
		case "/imports":
			out := []Record{}
			for _, batch := range q.list("imports") {
				if batch["actor"] == q.actor.Actor && (q.r.URL.Query().Get("clientBatchId") == "" || batch["clientBatchId"] == q.r.URL.Query().Get("clientBatchId")) {
					out = append(out, batch)
				}
			}
			return out, 200, nil
		case "/source-mappings":
			return q.list("sourceMappings"), 200, nil
		case "/session":
			projects := []Project{}
			for _, p := range q.s.cfg.Projects {
				if q.actor.Projects[p.ID] != "" {
					projects = append(projects, p)
				}
			}
			return Record{"actor": q.actor.Actor, "role": q.role, "projects": projects, "projectId": q.project, "permissions": q.actor.Permissions}, 200, nil
		case "/collection-drafts":
			return q.collectionDraftList(), 200, nil
		case "/capabilities":
			return q.capabilities(), 200, nil
		case "/workspace":
			out := Record{"capabilities": q.capabilities()}
			for _, kind := range []string{"issues", "assets", "parseRuns", "evidence", "turns", "verifications", "updates", "notifications", "sourceMappings", "hypotheses", "issueHistory"} {
				out[kind] = q.list(kind)
			}
			drafts := []Record{}
			for _, d := range q.list("drafts") {
				if d["actor"] == q.actor.Actor && d["usedAt"] == nil {
					drafts = append(drafts, d)
				}
			}
			for _, d := range q.list("editableDrafts") {
				if d["actor"] == q.actor.Actor {
					drafts = append(drafts, d)
				}
			}
			out["handoffDrafts"] = drafts
			out["collectionDrafts"] = q.collectionDraftList()
			return out, 200, nil
		case "/issues":
			return q.list("issues"), 200, nil
		case "/notifications":
			return q.list("notifications"), 200, nil
		case "/demo-fixtures":
			return []Record{{"id": "web-example", "name": "Synthetic Web investigation log", "origin": "demo", "platform": "web"}}, 200, nil
		case "/source-diffs":
			return q.sourceDiff()
		}
	}
	if method == "POST" {
		switch path {
		case "/issues":
			if strings.TrimSpace(str(b["title"])) == "" {
				return nil, 0, fail(422, "TITLE_REQUIRED", "Issue title is required")
			}
			r := q.s.state.add("issues", q.project, Record{"title": b["title"], "description": str(b["description"]), "owner": str(b["owner"]), "priority": str(b["priority"]), "context": record(b["context"]), "status": "investigating", "revision": 1, "contextRevision": 1, "verificationRevision": 1, "evidenceIds": []string{}, "assetIds": []string{}})
			q.history(r, nil, "created", "Issue created")
			return r, 201, nil
		case "/imports":
			return q.importFiles()
		case "/collection-drafts":
			return q.collectionDraft(nil, b)
		case "/collections":
			return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Aliyun push is deferred until server and client protocol configuration is provided")
		case "/source-mappings":
			return q.createMapping(b)
		case "/source-resolutions":
			return q.resolveSource(b)
		case "/exports":
			return q.export(b)
		}
	}
	if len(parts) >= 2 {
		kind, id := parts[0], parts[1]
		switch kind {
		case "collection-drafts":
			draft, e := q.get("collectionDrafts", id)
			if e != nil {
				return nil, 0, e
			}
			if draft["actor"] != q.actor.Actor {
				return nil, 0, fail(404, "NOT_FOUND", "Draft not found")
			}
			if len(parts) == 2 && method == "GET" {
				if q.expired(draft) {
					return Record{"id": id, "status": "draft", "availability": "expired", "revision": draft["revision"]}, 200, nil
				}
				return draft, 200, nil
			}
			if len(parts) == 2 && method == "PATCH" {
				return q.collectionDraft(draft, b)
			}

		case "legacy":
			if len(parts) == 6 && parts[1] == "tasks" && parts[4] == "details" && method == "GET" {
				if q.s.cfg.Legacy == nil {
					return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Read-only legacy adapter is not configured")
				}
				taskID, te := strconv.ParseInt(parts[3], 10, 64)
				detailID, de := strconv.ParseInt(parts[5], 10, 64)
				if te != nil || de != nil {
					return nil, 0, fail(422, "LEGACY_ID_INVALID", "Use original decimal task and detail IDs")
				}
				var detail connectors.LegacyDetail
				if e := q.external(func() error {
					var err error
					detail, err = q.s.cfg.Legacy.Detail(q.r.Context(), q.project, parts[2], taskID, detailID)
					return err
				}); e != nil {
					return nil, 0, e
				}
				return detail, 200, nil
			}
		case "operations":
			op, err := q.get("operations", id)
			if err != nil {
				return nil, 0, err
			}
			if op["actor"] != q.actor.Actor {
				return nil, 0, fail(404, "NOT_FOUND", "Operation not found")
			}
			if method == "GET" {
				return op, 200, nil
			}
		case "imports":
			batch, err := q.get("imports", id)
			if err != nil {
				return nil, 0, err
			}
			if batch["actor"] != q.actor.Actor {
				return nil, 0, fail(404, "NOT_FOUND", "Import not found")
			}
			if method == "GET" && len(parts) == 2 {
				return batch, 200, nil
			}
			if method == "POST" && len(parts) == 3 && parts[2] == "cancel" {
				batch["cancelRequestedAt"] = now()
				batch["revision"] = num(batch["revision"]) + 1
				return batch, 200, nil
			}
		case "issues":
			issue, err := q.get("issues", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 2 {
				if method == "GET" {
					return issue, 200, nil
				}
				if method == "PATCH" {
					if e = q.match(issue, "revision"); e != nil {
						return nil, 0, e
					}
					prior := clone(issue)
					if status := str(b["status"]); status != "" {
						if status != str(issue["status"]) && strings.TrimSpace(str(b["reason"])) == "" {
							return nil, 0, fail(422, "STATUS_REASON_REQUIRED", "Explain this manual status change")
						}
						if e = q.transition(issue, status); e != nil {
							return nil, 0, e
						}
						issue["status"] = status
						if status == "reopened" {
							issue["lastReopenedAt"] = now()
						}
					}
					for _, key := range []string{"title", "description", "owner", "priority", "context"} {
						if v, ok := b[key]; ok {
							if key == "title" && strings.TrimSpace(str(v)) == "" {
								return nil, 0, fail(422, "TITLE_REQUIRED", "Issue title is required")
							}
							issue[key] = v
						}
					}
					issue["revision"] = num(issue["revision"]) + 1
					issue["updatedAt"] = now()
					q.history(issue, prior, "edited", str(b["reason"]))
					return issue, 200, nil
				}
			}
			if len(parts) == 3 {
				switch parts[2] {
				case "log-links":
					if method == "POST" {
						return q.linkAsset(issue, b)
					}
				case "evidence":
					if method == "POST" {
						return q.createEvidence(issue, b)
					}
				case "context":
					if method == "PATCH" {
						if e = q.match(issue, "contextRevision"); e != nil {
							return nil, 0, e
						}
						ids := stringsOf(issue["evidenceIds"])
						for _, eid := range stringsOf(b["add"]) {
							ev, err := q.get("evidence", eid)
							if err != nil {
								return nil, 0, err
							}
							if ev["issueId"] != id {
								return nil, 0, fail(422, "EVIDENCE_SCOPE", "Evidence belongs to another issue")
							}
							ids = append(ids, eid)
						}
						remove := stringsOf(b["remove"])
						kept := []string{}
						for _, eid := range unique(ids) {
							if !contains(remove, eid) {
								kept = append(kept, eid)
							}
						}
						issue["evidenceIds"] = kept
						issue["contextRevision"] = num(issue["contextRevision"]) + 1
						return issue, 200, nil
					}
				case "turns":
					if method == "POST" {
						return q.createTurn(issue, b, nil)
					}
					if method == "GET" {
						return q.forIssue("turns", id), 200, nil
					}
				case "history":
					if method == "GET" {
						return q.forIssue("issueHistory", id), 200, nil
					}
				case "hypotheses":
					if method == "GET" {
						return q.forIssue("hypotheses", id), 200, nil
					}
					if method == "POST" {
						return q.createHypothesis(issue, b)
					}
				case "verifications":
					if method == "GET" {
						return q.forIssue("verifications", id), 200, nil
					}
					if method == "POST" {
						return q.verify(issue, b)
					}
				case "handoff-drafts":
					if method == "POST" {
						return q.saveDraft(issue, b)
					}
				case "handoff-previews":
					if method == "POST" {
						return q.preview(issue, b)
					}
				case "handoffs":
					if method == "POST" {
						return q.handoff(issue, b)
					}
				}
			}
		case "assets":
			a, err := q.get("assets", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 3 {
				switch parts[2] {
				case "raw":
					if method == "GET" {
						if e = q.permission("raw"); e != nil {
							return nil, 0, e
						}
						raw, err := q.raw(a)
						if err != nil {
							return nil, 0, err
						}
						q.download = raw
						q.filename = str(a["name"])
						q.contentType = "application/octet-stream"
						return nil, 200, nil
					}
				case "parse-runs":
					if method == "POST" {
						return q.startParse(a, b)
					}
					if method == "GET" {
						out := []Record{}
						for _, r := range q.list("parseRuns") {
							if r["assetId"] == id {
								out = append(out, r)
							}
						}
						return out, 200, nil
					}
				}
			}
		case "parse-runs":
			run, err := q.get("parseRuns", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 2 && method == "GET" {
				return run, 200, nil
			}
			if len(parts) == 3 {
				switch parts[2] {
				case "cancel":
					if method == "POST" {
						if run["status"] == "queued" || run["status"] == "running" {
							run["status"] = "canceled"
							run["finishedAt"] = now()
							q.afterCommit = append(q.afterCommit, func() {
								if c := q.s.cancels[id]; c != nil {
									c()
								}
							})
						}
						return run, 200, nil
					}
				case "comparison":
					if method == "GET" {
						return q.compareRuns(run)
					}
				case "events", "histogram":
					if method == "GET" {
						return q.readEvents(run, parts[2])
					}
				}
			}
		case "events":
			event, err := q.get("events", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 3 && method == "GET" {
				switch parts[2] {
				case "context":
					return q.contextEvents(event)
				case "related":
					return q.related(event)
				}
			}
		case "handoff-drafts":
			v, err := q.get("drafts", id)
			if err != nil {
				v, err = q.get("editableDrafts", id)
			}
			if err != nil {
				return nil, 0, err
			}
			if v["actor"] != q.actor.Actor {
				return nil, 0, fail(404, "NOT_FOUND", "Draft not found")
			}
			if method == "GET" {
				return v, 200, nil
			}
			if method == "PATCH" {
				if v["editable"] != true {
					return nil, 0, fail(409, "PREVIEW_IMMUTABLE", "Create an editable draft to change text")
				}
				if q.hasSourceRefs(v, map[string]bool{}) && q.permission("source") != nil {
					return nil, 0, fail(403, "DRAFT_RESTRICTED", "Draft depends on restricted source content; use current authorized evidence in a new draft")
				}
				if q.expired(v) || q.hasExpiredRefs(v, map[string]bool{}) {
					return nil, 0, fail(410, "DRAFT_EXPIRED", "Editable draft expired; create a new draft with current evidence and fresh text")
				}
				if e := q.match(v, "revision"); e != nil {
					return nil, 0, e
				}
				copyDraftFields(v, b)
				q.refreshDraftDependencies(v)
				v["revision"] = num(v["revision"]) + 1
				return v, 200, nil
			}

		case "evidence-snapshots":
			if method == "GET" {
				v, err := q.get("snapshots", id)
				return v, 200, err
			}
		case "source-fragments":
			if method == "GET" {
				if e = q.permission("source"); e != nil {
					return nil, 0, e
				}
				v, err := q.get("sourceFragments", id)
				return v, 200, err
			}
		case "turns":
			turn, err := q.get("turns", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 2 && method == "GET" {
				return turn, 200, nil
			}
			if len(parts) == 3 && method == "POST" {
				switch parts[2] {
				case "cancel":
					if turn["status"] == "queued" || turn["status"] == "running" {
						turn["status"] = "canceled"
						turn["finishedAt"] = now()
						q.afterCommit = append(q.afterCommit, func() {
							if c := q.s.cancels[id]; c != nil {
								c()
							}
						})
					}
					return turn, 200, nil
				case "retries":
					issue, err := q.get("issues", str(turn["issueId"]))
					if err != nil {
						return nil, 0, err
					}
					if !contains([]string{"failed", "canceled", "interrupted"}, str(turn["status"])) {
						return nil, 0, fail(409, "TURN_RETRY_INVALID", "Only failed, canceled or interrupted turns may be retried")
					}
					return q.createTurn(issue, Record{"question": turn["question"], "parentTurnId": turn["parentTurnId"], "includeSource": turn["includeSource"], "retryOf": turn["id"]}, record(turn["snapshot"]))
				}
			}
		case "notifications":
			n, err := q.get("notifications", id)
			if err != nil {
				return nil, 0, err
			}
			if len(parts) == 3 && parts[2] == "read" && method == "POST" {
				n["read"] = true
				n["readAt"] = now()
				n["revision"] = num(n["revision"]) + 1
				return n, 200, nil
			}
		case "exports":
			if len(parts) == 2 && method == "GET" {
				exp, e := q.get("exports", id)
				if e != nil {
					return nil, 0, e
				}
				copy := clone(exp)
				delete(copy, "content")
				delete(copy, "payload")
				copy["status"] = "completed"
				return copy, 200, nil
			}
			if len(parts) == 3 && parts[2] == "content" && method == "GET" {
				if e = q.permission("export"); e != nil {
					return nil, 0, e
				}
				exp, err := q.get("exports", id)
				if err != nil {
					return nil, 0, err
				}
				if q.expired(exp) {
					return nil, 0, fail(410, "SOURCE_EXPIRED", "Export expired")
				}
				if exp["kind"] == "raw" {
					if e := q.permission("raw"); e != nil {
						return nil, 0, e
					}
					asset, e := q.get("assets", str(exp["assetId"]))
					if e != nil {
						return nil, 0, e
					}
					raw, e := q.raw(asset)
					if e != nil {
						return nil, 0, e
					}
					q.download = raw
					q.filename = str(asset["name"])
					q.contentType = "application/octet-stream"
					return nil, 200, nil
				}
				q.download = []byte(str(exp["content"]))
				if exp["payload"] != nil {
					q.download, _ = json.MarshalIndent(q.redact(exp["payload"]), "", "  ")
				}
				q.filename = "logan-export-" + id + ".json"
				q.contentType = "application/json"
				return nil, 200, nil
			}
		case "demo-fixtures":
			if id == "web-example" && len(parts) == 3 {
				if parts[2] == "download" && method == "GET" {
					q.download = []byte(demoContent)
					q.filename = "logan-demo.jsonl"
					q.contentType = "application/x-ndjson"
					return nil, 200, nil
				}
				if parts[2] == "load" && method == "POST" {
					if !q.isDemo() {
						return nil, 0, fail(403, "DEMO_PROJECT_REQUIRED", "Load synthetic fixtures only in a demo project")
					}
					a, err := q.addAsset("logan-demo.jsonl", []byte(demoContent), Record{"origin": "demo", "platform": "web", "build": "demo"})
					if err != nil {
						return nil, 0, err
					}
					return Record{"assets": []Record{a}}, 201, nil
				}
			}
		}
	}
	return nil, 0, fail(404, "NOT_FOUND", "Unknown workbench route")
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func unique(a []string) []string {
	out := []string{}
	for _, v := range a {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
func (q *request) forIssue(kind, id string) []Record {
	out := []Record{}
	for _, r := range q.list(kind) {
		if r["issueId"] == id {
			out = append(out, r)
		}
	}
	return out
}
func (q *request) isDemo() bool {
	for _, p := range q.s.cfg.Projects {
		if p.ID == q.project {
			return p.Demo
		}
	}
	return false
}
func (q *request) expired(r Record) bool {
	t, err := time.Parse(time.RFC3339Nano, str(r["expiresAt"]))
	return err == nil && time.Now().After(t)
}
func (q *request) transition(issue Record, status string) *apiError {
	if !contains([]string{"investigating", "awaiting_fix", "awaiting_verification", "resolved", "reopened"}, status) {
		return fail(422, "INVALID_STATUS", "Unsupported issue status")
	}
	old := str(issue["status"])
	if status == old {
		return nil
	}
	if old == "resolved" && status != "reopened" {
		return fail(422, "REOPEN_REQUIRED", "Resolved issues must be reopened first")
	}
	if status == "resolved" {
		if q.role != "reviewer" && q.role != "admin" {
			return fail(403, "REVIEWER_REQUIRED", "Reviewer role required to resolve")
		}
		if old != "awaiting_verification" {
			return fail(422, "INVALID_TRANSITION", "Resolve only after awaiting verification")
		}
		valid := false
		latestSequence := -1
		for _, v := range q.forIssue("verifications", str(issue["id"])) {
			if v["purpose"] == "fix" && num(v["sequence"]) > latestSequence {
				latestSequence = num(v["sequence"])
				if q.hasSourceRefs(v, map[string]bool{}) && q.permission("source") != nil {
					return fail(403, "PERMISSION_FORBIDDEN", "Fix verification depends on restricted source evidence")
				}
				valid = v["result"] == "pass" && !q.hasExpiredRefs(v, map[string]bool{}) && len(stringsOf(v["evidenceIds"])) > 0 && (str(issue["lastReopenedAt"]) == "" || afterTimestamp(str(v["createdAt"]), str(issue["lastReopenedAt"])))
				for _, eid := range stringsOf(v["evidenceIds"]) {
					ev, err := q.get("evidence", eid)
					if err != nil || q.expired(ev) {
						valid = false
					}
				}
			}
		}
		if !valid {
			return fail(422, "FIX_VERIFICATION_REQUIRED", "A passing fix verification is required")
		}
	}
	return nil
}
func (q *request) snapshot(issue Record) Record {
	evs := []Record{}
	ids := stringsOf(issue["evidenceIds"])
	for _, eid := range ids {
		if ev, err := q.get("evidence", eid); err == nil {
			evs = append(evs, clone(ev))
		}
	}
	return q.s.state.add("snapshots", q.project, Record{"issueId": issue["id"], "contextRevision": issue["contextRevision"], "evidenceIds": ids, "evidence": evs, "expiresAt": earliestExpiry(Record{"evidence": evs}, q.s.cfg.Retention)})
}
func (q *request) verify(issue, b Record) (any, int, *apiError) {
	purpose, result := str(b["purpose"]), str(b["result"])
	if purpose != "fix" && purpose != "hypothesis" {
		return nil, 0, fail(422, "INVALID_VERIFICATION", "Purpose must be fix or hypothesis")
	}
	if (purpose == "fix" && !contains([]string{"pass", "fail"}, result)) || (purpose == "hypothesis" && !contains([]string{"supported", "refuted", "uncertain"}, result)) {
		return nil, 0, fail(422, "INVALID_VERIFICATION", "Result does not match purpose")
	}
	if purpose == "hypothesis" {
		h, e := q.get("hypotheses", str(b["hypothesisId"]))
		if e != nil {
			return nil, 0, e
		}
		if h["issueId"] != issue["id"] || num(b["hypothesisVersion"]) != num(h["version"]) {
			return nil, 0, fail(422, "HYPOTHESIS_SCOPE", "Select the current hypothesis version from this issue")
		}
		if q.expired(h) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Hypothesis expired")
		}
		h["status"] = result
		h["revision"] = num(h["revision"]) + 1
	}
	for _, field := range []string{"method", "expected", "observed"} {
		if strings.TrimSpace(str(b[field])) == "" {
			return nil, 0, fail(422, "INVALID_VERIFICATION", field+" is required")
		}
	}
	if purpose == "fix" && (str(b["build"]) == "" || str(b["scenario"]) == "") {
		return nil, 0, fail(422, "INVALID_VERIFICATION", "Fix verification needs build and scenario")
	}
	if purpose == "fix" && len(stringsOf(b["evidenceIds"])) == 0 {
		return nil, 0, fail(422, "FIX_EVIDENCE_REQUIRED", "Fix verification requires at least one evidence item")
	}
	for _, eid := range stringsOf(b["evidenceIds"]) {
		ev, err := q.get("evidence", eid)
		if err != nil {
			return nil, 0, err
		}
		if ev["issueId"] != issue["id"] {
			return nil, 0, fail(422, "EVIDENCE_SCOPE", "Evidence must belong to this issue")
		}
		if q.hasSourceRefs(ev, map[string]bool{}) && q.permission("source") != nil {
			return nil, 0, fail(403, "PERMISSION_FORBIDDEN", "Verification references restricted source evidence")
		}
		if q.hasExpiredRefs(ev, map[string]bool{}) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Verification evidence expired")
		}
	}
	b["sequence"] = num(issue["verificationRevision"]) + 1
	b["issueId"] = issue["id"]
	b["expiresAt"] = q.dependencyExpiry(Record{"evidenceIds": b["evidenceIds"], "hypothesisId": b["hypothesisId"]})
	b["actor"] = q.actor.Actor
	b["evidenceIds"] = stringsOf(b["evidenceIds"])
	v := q.s.state.add("verifications", q.project, b)
	issue["verificationRevision"] = num(issue["verificationRevision"]) + 1
	raw, _ := json.Marshal(v)
	ev := q.s.state.add("evidence", q.project, Record{"issueId": issue["id"], "kind": "verification", "label": purpose + " verification: " + result, "originRef": Record{"verificationId": v["id"], "evidenceIds": v["evidenceIds"], "hypothesisId": v["hypothesisId"], "hypothesisVersion": v["hypothesisVersion"]}, "content": string(raw), "contentHash": digest(raw), "verified": false, "availability": "available", "expiresAt": v["expiresAt"], "actor": q.actor.Actor})
	issue["evidenceIds"] = append(stringsOf(issue["evidenceIds"]), str(ev["id"]))
	issue["contextRevision"] = num(issue["contextRevision"]) + 1
	return v, 201, nil
}
func (q *request) preview(issue, b Record) (any, int, *apiError) {
	for _, eid := range stringsOf(issue["evidenceIds"]) {
		ev, err := q.get("evidence", eid)
		if err != nil || q.expired(ev) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Investigation evidence expired; remove or replace it before previewing")
		}
	}
	if q.hasSourceRefs(issue, map[string]bool{}) && q.permission("source") != nil {
		return nil, 0, fail(403, "PERMISSION_FORBIDDEN", "Preview evidence requires source access")
	}
	if num(b["issueRevision"]) != num(issue["revision"]) || num(b["contextRevision"]) != num(issue["contextRevision"]) {
		return nil, 0, fail(409, "DRAFT_STALE", "Issue or evidence changed; create a fresh preview")
	}
	mode := str(b["mode"])
	if mode != "manual" && mode != "ai" {
		return nil, 0, fail(422, "INVALID_MODE", "Choose manual or ai")
	}
	if strings.TrimSpace(str(b["summary"])) == "" {
		return nil, 0, fail(422, "SUMMARY_REQUIRED", "Summary is required")
	}
	if mode == "ai" {
		turn, err := q.get("turns", str(b["turnId"]))
		if err != nil {
			return nil, 0, err
		}
		if q.hasExpiredRefs(turn, map[string]bool{}) || q.snapshotUnavailable(record(turn["snapshot"])) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "AI source turn expired")
		}
		if q.hasSourceRefs(turn, map[string]bool{}) && q.permission("source") != nil {
			return nil, 0, fail(403, "PERMISSION_FORBIDDEN", "AI source turn requires source permission")
		}
		if turn["issueId"] != issue["id"] || turn["status"] != "completed" {
			return nil, 0, fail(422, "TURN_INCOMPLETE", "AI handoff requires a completed turn from this issue")
		}
	} else {
		b["turnId"] = nil
	}
	if e := q.transition(issue, str(b["status"])); e != nil {
		return nil, 0, e
	}
	b["issueId"] = issue["id"]
	b["snapshot"] = q.snapshot(issue)
	b["verificationRevision"] = issue["verificationRevision"]
	b["verificationIds"] = []string{}
	for _, v := range q.forIssue("verifications", str(issue["id"])) {
		if q.hasExpiredRefs(v, map[string]bool{}) {
			continue
		}
		if v["purpose"] == "fix" && str(issue["lastReopenedAt"]) != "" && !afterTimestamp(str(v["createdAt"]), str(issue["lastReopenedAt"])) {
			continue
		}
		b["verificationIds"] = append(stringsOf(b["verificationIds"]), str(v["id"]))
	}
	b["expiresAt"] = q.dependencyExpiry(b)
	b["version"] = 1
	b["actor"] = q.actor.Actor
	raw, _ := json.Marshal(b)
	b["hash"] = digest(raw)
	return q.s.state.add("drafts", q.project, b), 201, nil
}
func (q *request) handoff(issue, b Record) (any, int, *apiError) {
	if e := q.match(issue, "revision"); e != nil {
		return nil, 0, e
	}
	draft, e := q.get("drafts", str(b["draftId"]))
	if e != nil {
		return nil, 0, e
	}
	if q.hasSourceRefs(draft, map[string]bool{}) && q.permission("source") != nil {
		return nil, 0, fail(403, "PERMISSION_FORBIDDEN", "Preview source permission was revoked")
	}
	if q.hasExpiredRefs(draft, map[string]bool{}) || q.snapshotUnavailable(record(draft["snapshot"])) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Preview evidence expired; replace it and create a fresh preview")
	}
	if draft["mode"] == "ai" {
		turn, err := q.get("turns", str(draft["turnId"]))
		if err != nil || q.hasExpiredRefs(turn, map[string]bool{}) || q.snapshotUnavailable(record(turn["snapshot"])) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "AI source turn expired")
		}
	}
	if b["confirmed"] != true {
		return nil, 0, fail(422, "CONFIRMATION_REQUIRED", "Explicit human confirmation is required")
	}
	if draft["issueId"] != issue["id"] || draft["actor"] != q.actor.Actor {
		return nil, 0, fail(403, "DRAFT_FORBIDDEN", "Draft belongs to another issue or actor")
	}
	if draft["usedAt"] != nil {
		return nil, 0, fail(409, "DRAFT_ALREADY_SAVED", "This preview was already saved")
	}
	if str(b["hash"]) != str(draft["hash"]) || num(b["version"]) != num(draft["version"]) {
		return nil, 0, fail(409, "DRAFT_CONFLICT", "Preview identity changed")
	}
	for _, field := range []string{"issueRevision", "contextRevision", "verificationRevision"} {
		current := field
		if field == "issueRevision" {
			current = "revision"
		}
		if num(draft[field]) != num(issue[current]) {
			return nil, 0, fail(409, "DRAFT_STALE", "Issue, evidence or verification changed; draft text is preserved")
		}
	}
	if e = q.transition(issue, str(draft["status"])); e != nil {
		return nil, 0, e
	}
	prior := clone(issue)
	update := clone(draft)
	delete(update, "id")
	delete(update, "createdAt")
	update["draftId"] = draft["id"]
	update = q.s.state.add("updates", q.project, update)
	draft["usedAt"] = now()
	issue["status"] = draft["status"]
	if draft["status"] == "reopened" {
		issue["lastReopenedAt"] = now()
	}
	issue["owner"] = str(draft["owner"])
	issue["revision"] = num(issue["revision"]) + 1
	history := q.history(issue, prior, "handoff", str(draft["summary"]))
	history["turnId"] = draft["turnId"]
	history["evidenceIds"] = record(draft["snapshot"])["evidenceIds"]
	history["expiresAt"] = q.dependencyExpiry(draft)
	q.notify(str(issue["id"]), "handoff", "Investigation update saved")
	return update, 201, nil
}
