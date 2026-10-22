package workbench

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"logan/servergo/internal/workbench/connectors"
)

func (q *request) createEvidence(issue, b Record) (any, int, *apiError) {
	kind := str(b["kind"])
	origin := record(b["originRef"])
	content := str(b["content"])
	verified := false
	expiry := time.Now().UTC().Add(q.s.cfg.Retention).Format(time.RFC3339Nano)
	switch kind {
	case "log":
		run, err := q.get("parseRuns", str(origin["runId"]))
		if err != nil {
			return nil, 0, err
		}
		if q.expired(run) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Selected log expired")
		}
		eventIDs := stringsOf(origin["eventIds"])
		if len(eventIDs) == 0 || len(eventIDs) > 100 {
			return nil, 0, fail(422, "EVIDENCE_RANGE", "Select between 1 and 100 events")
		}
		evs := []Record{}
		for _, eid := range eventIDs {
			ev, e := q.get("events", eid)
			if e != nil {
				return nil, 0, e
			}
			if ev["runId"] != run["id"] {
				return nil, 0, fail(422, "EVIDENCE_SCOPE", "Events must belong to the selected parse run")
			}
			evs = append(evs, ev)
		}
		raw, _ := json.Marshal(evs)
		content = string(raw)
		verified = true
		expiry = str(run["expiresAt"])
	case "source":
		if e := q.permission("source"); e != nil {
			return nil, 0, e
		}
		fragment, e := q.get("sourceFragments", str(origin["fragmentId"]))
		if e != nil {
			return nil, 0, e
		}
		if q.hasExpiredRefs(fragment, map[string]bool{}) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Source fragment expired")
		}
		content = str(fragment["content"])
		verified = fragment["verified"] == true
		origin = clone(fragment)
		delete(origin, "content")
		expiry = str(fragment["expiresAt"])
	case "manual":
		if strings.TrimSpace(content) == "" {
			return nil, 0, fail(422, "CONTENT_REQUIRED", "Manual evidence needs content")
		}
		verified = false
	default:
		return nil, 0, fail(422, "EVIDENCE_KIND", "Choose log, source or manual evidence")
	}
	if len(content) > 256<<10 {
		return nil, 0, fail(413, "EVIDENCE_LIMIT", "Evidence excerpt exceeds 256 KiB")
	}
	ev := q.s.state.add("evidence", q.project, Record{"issueId": issue["id"], "kind": kind, "label": str(b["label"]), "originRef": origin, "content": content, "contentHash": digest([]byte(content)), "availability": "available", "verified": verified, "expiresAt": expiry, "actor": q.actor.Actor})
	issue["evidenceIds"] = unique(append(stringsOf(issue["evidenceIds"]), str(ev["id"])))
	issue["contextRevision"] = num(issue["contextRevision"]) + 1
	return ev, 201, nil
}
func (q *request) createMapping(b Record) (any, int, *apiError) {
	if q.role != "admin" {
		return nil, 0, fail(403, "ADMIN_REQUIRED", "Only project administrators may trust a build mapping")
	}
	if e := q.permission("source"); e != nil {
		return nil, 0, e
	}
	for _, field := range []string{"app", "platform", "build", "repoId", "commit"} {
		if str(b[field]) == "" {
			return nil, 0, fail(422, "MAPPING_INCOMPLETE", field+" is required")
		}
	}
	commit := str(b["commit"])
	if len(commit) != 40 && len(commit) != 64 {
		return nil, 0, fail(422, "COMMIT_INVALID", "Use a full immutable commit hash")
	}
	for _, c := range commit {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return nil, 0, fail(422, "COMMIT_INVALID", "Use a lowercase immutable commit hash")
		}
	}
	for _, old := range q.list("sourceMappings") {
		if old["app"] == b["app"] && old["platform"] == b["platform"] && old["build"] == b["build"] {
			return nil, 0, fail(409, "MAPPING_EXISTS", "Build mapping is immutable; choose a new build identity")
		}
	}
	if e := q.external(func() error { return q.s.cfg.Source.ValidateCommit(q.r.Context(), str(b["repoId"]), commit) }); e != nil {
		return nil, 0, e
	}
	b["actor"] = q.actor.Actor
	b["verified"] = true
	return q.s.state.add("sourceMappings", q.project, b), 201, nil
}
func (q *request) resolveSource(b Record) (any, int, *apiError) {
	if e := q.permission("source"); e != nil {
		return nil, 0, e
	}
	event, e := q.get("events", str(b["eventId"]))
	if e != nil {
		return nil, 0, e
	}
	if q.expired(event) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Event expired")
	}
	run, _ := q.get("parseRuns", str(event["runId"]))
	asset, _ := q.get("assets", str(run["assetId"]))
	mappingID := str(b["mappingId"])
	var mapping Record
	if mappingID != "" {
		mapping, e = q.get("sourceMappings", mappingID)
		if e != nil {
			return nil, 0, e
		}
	} else {
		for _, m := range q.list("sourceMappings") {
			if m["build"] == asset["build"] && m["platform"] == asset["platform"] && m["app"] == asset["app"] {
				mapping = m
				break
			}
		}
	}
	if mapping == nil {
		return Record{"status": "missing", "verified": false, "reason": "No trusted immutable build mapping; asset build/app identity may be unknown"}, 200, nil
	}
	if str(asset["build"]) == "" || str(asset["app"]) == "" || mapping["build"] != asset["build"] || mapping["app"] != asset["app"] || mapping["platform"] != asset["platform"] {
		return Record{"status": "candidate", "verified": false, "mappingId": mapping["id"], "reason": "Mapping does not match this asset's independently recorded app/platform/build"}, 200, nil
	}
	path := str(b["path"])
	if path == "" {
		path = str(event["file"])
	}
	if path == "" {
		return Record{"status": "candidate", "verified": false, "mappingId": mapping["id"], "reason": "Build matched; location is unknown"}, 200, nil
	}
	start, end := num(b["start"]), num(b["end"])
	if start <= 0 {
		start = num(event["sourceLine"])
	}
	if start <= 0 {
		start = 1
	}
	if end <= 0 {
		end = start + 20
	}
	var fragment connectors.Fragment
	if e := q.external(func() error {
		var err error
		fragment, err = q.s.cfg.Source.Read(q.r.Context(), connectors.SourceRequest{RepoID: str(mapping["repoId"]), Commit: str(mapping["commit"]), Path: path, Start: start, End: end})
		return err
	}); e != nil {
		return nil, 0, e
	}
	boundPath := str(event["file"]) != "" && str(event["file"]) == path
	boundLine := num(event["sourceLine"]) > 0 && num(event["sourceLine"]) >= start && num(event["sourceLine"]) <= end
	r := Record{}
	raw, _ := json.Marshal(fragment)
	_ = json.Unmarshal(raw, &r)
	r["mappingId"] = mapping["id"]
	r["identitySource"] = asset["identitySource"]
	r["app"] = asset["app"]
	r["platform"] = asset["platform"]
	r["build"] = asset["build"]
	r["version"] = asset["version"]
	r["assetId"] = asset["id"]
	r["eventId"] = event["id"]
	r["status"] = "candidate"
	r["verified"] = false
	r["reason"] = "Build matched and fragment exists; client-selected path/line is not bound to the event"
	if boundPath && boundLine {
		r["status"] = "matched"
		r["verified"] = true
		r["reason"] = "Administrator build mapping agrees with operator-declared asset identity and event-reported path/line; no CI or SDK identity authentication is claimed"
	}
	r["expiresAt"] = asset["expiresAt"]
	r = q.s.state.add("sourceFragments", q.project, r)
	return r, 201, nil
}
func (q *request) sourceDiff() (any, int, *apiError) {
	if e := q.permission("source"); e != nil {
		return nil, 0, e
	}
	left, e := q.get("sourceFragments", q.r.URL.Query().Get("leftRef"))
	if e != nil {
		return nil, 0, e
	}
	right, e := q.get("sourceFragments", q.r.URL.Query().Get("rightRef"))
	if e != nil {
		return nil, 0, e
	}
	if q.expired(left) || q.expired(right) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Source fragment expired")
	}
	if left["repoId"] != right["repoId"] || left["path"] != right["path"] {
		return nil, 0, fail(422, "DIFF_IDENTITY", "Compare the same repository and path")
	}
	var d connectors.Diff
	if e := q.external(func() error {
		var err error
		d, err = q.s.cfg.Source.Diff(q.r.Context(), connectors.DiffRequest{RepoID: str(left["repoId"]), LeftCommit: str(left["commit"]), RightCommit: str(right["commit"]), Path: str(left["path"])})
		return err
	}); e != nil {
		return nil, 0, e
	}
	if q.hasExpiredRefs(left, map[string]bool{}) || q.hasExpiredRefs(right, map[string]bool{}) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Source fragment expired during comparison")
	}
	return d, 200, nil
}
func (q *request) createTurn(issue, b, fixed Record) (any, int, *apiError) {
	if e := q.permission("model"); e != nil {
		return nil, 0, e
	}
	question := strings.TrimSpace(str(b["question"]))
	if question == "" || len(question) > 16000 {
		return nil, 0, fail(422, "QUESTION_INVALID", "Question must be 1–16000 bytes")
	}
	if fixed == nil && num(b["contextRevision"]) != num(issue["contextRevision"]) {
		return nil, 0, fail(409, "CONTEXT_CONFLICT", "Evidence scope changed; reload before asking")
	}
	if parent := str(b["parentTurnId"]); parent != "" {
		p, e := q.get("turns", parent)
		if e != nil {
			return nil, 0, e
		}
		if q.snapshotUnavailable(record(p["snapshot"])) || q.expired(p) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Parent turn content expired")
		}
		if q.hasSourceRefs(p, map[string]bool{}) && q.permission("source") != nil {
			return nil, 0, fail(403, "PERMISSION_FORBIDDEN", "Parent answer depends on source evidence")
		}
		if p["issueId"] != issue["id"] || p["status"] != "completed" {
			return nil, 0, fail(422, "PARENT_TURN_INVALID", "Follow-up requires a completed turn from this issue")
		}
	}
	if fixed != nil {
		for _, v := range sliceOf(fixed["evidence"]) {
			if q.expired(record(v)) {
				return nil, 0, fail(410, "SOURCE_EXPIRED", "Retry snapshot expired; create a new turn with new evidence")
			}
		}
	}
	snapshot := fixed
	if fixed == nil {
		snapshot = q.snapshot(issue)
	} else {
		snapshot = clone(fixed)
	}
	evidence := []connectors.Evidence{}
	manifest := []Record{}
	total := 0
	for _, value := range sliceOf(snapshot["evidence"]) {
		ev := record(value)
		included := true
		reason := ""
		if q.expired(ev) {
			included = false
			reason = "expired"
		}
		if q.hasSourceRefs(ev, map[string]bool{}) && q.permission("source") != nil {
			included = false
			reason = "source_permission"
		}
		content := str(ev["content"])
		if total+len(content) > 192<<10 {
			included = false
			reason = "input_budget"
		}
		if included {
			total += len(content)
			evidence = append(evidence, q.modelEvidence(ev))
		}
		manifest = append(manifest, Record{"evidenceId": ev["id"], "included": included, "reason": reason, "originalContentHash": ev["contentHash"], "sentContentHash": digest([]byte(maskText(content))), "sentBytes": len(maskText(content)), "maskingPolicy": "default-v1", "verified": ev["verified"], "availability": ev["availability"], "originRef": ev["originRef"]})
	}
	activeCount := 0
	for _, active := range q.s.state.bucket("turns") {
		if active["status"] == "queued" || active["status"] == "running" {
			activeCount++
		}
	}
	if activeCount >= 2 {
		return nil, 0, fail(429, "MODEL_CAPACITY", "Two model requests are active; retry after one finishes")
	}
	for _, active := range q.forIssue("turns", str(issue["id"])) {
		if active["status"] == "queued" || active["status"] == "running" {
			return nil, 0, fail(409, "TURN_IN_PROGRESS", "An investigation turn is already active for this issue")
		}
	}
	status := "queued"
	var turnError any
	if !q.modelReady() {
		status = "failed"
		turnError = Record{"code": "CONNECTOR_UNCONFIGURED", "message": "Model provider is not configured; manual investigation remains available"}
	}
	turn := q.s.state.add("turns", q.project, Record{"issueId": issue["id"], "question": question, "parentTurnId": b["parentTurnId"], "status": status, "snapshot": snapshot, "answer": nil, "error": turnError, "actor": q.actor.Actor, "retrievalManifest": manifest, "promptVersion": "workbench-1", "expiresAt": q.dependencyExpiry(Record{"snapshot": snapshot, "parentTurnId": b["parentTurnId"]}), "revision": 1})
	if status == "queued" {
		tid := str(turn["id"])
		in := connectors.Investigation{Question: maskText(question), Evidence: evidence, PriorRounds: []connectors.PriorRound{}}
		if parent := str(b["parentTurnId"]); parent != "" {
			p, _ := q.get("turns", parent)
			var answer connectors.Answer
			bytes, _ := json.Marshal(p["answer"])
			_ = json.Unmarshal(bytes, &answer)
			safeBytes, _ := json.Marshal(maskValue(p["answer"]))
			_ = json.Unmarshal(safeBytes, &answer)
			in.PriorRounds = append(in.PriorRounds, connectors.PriorRound{Question: maskText(str(p["question"])), Answer: answer})
		}
		q.afterCommit = append(q.afterCommit, func() { q.s.launchTurn(tid, in, digest([]byte(q.actor.Token))) })
	}
	return turn, 201, nil
}
func (s *Server) launchTurn(tid string, in connectors.Investigation, authHash string) {
	ctx, cancel := context.WithTimeout(s.ctx, 120*time.Second)
	s.cancels[tid] = cancel
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		defer cancel()
		s.mu.Lock()
		turn := s.state.bucket("turns")[tid]
		if turn["status"] != "queued" || s.closed {
			delete(s.cancels, tid)
			s.mu.Unlock()
			return
		}
		if e := s.validateTurn(turn, authHash); e != nil {
			s.persistJob(func() { turn["status"] = "failed"; turn["error"] = Record{"code": e.code, "message": e.message} })
			delete(s.cancels, tid)
			s.mu.Unlock()
			return
		}
		if !s.persistJob(func() { turn["status"] = "running"; turn["startedAt"] = now() }) {
			delete(s.cancels, tid)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		answer, err := s.cfg.Model.Investigate(ctx, in)
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.cancels, tid)
		turn = s.state.bucket("turns")[tid]
		if turn["status"] != "running" {
			return
		}
		if e := s.validateTurn(turn, authHash); e != nil {
			s.persistJob(func() {
				turn["status"] = "failed"
				turn["error"] = Record{"code": e.code, "message": e.message}
				turn["finishedAt"] = now()
				turn["answer"] = nil
			})
			return
		}
		s.persistJob(func() {
			turn["finishedAt"] = now()
			turn["revision"] = num(turn["revision"]) + 1
			if err != nil {
				code := "MODEL_FAILED"
				if ctx.Err() != nil {
					turn["status"] = "canceled"
					code = "MODEL_CANCELED"
				} else {
					turn["status"] = "failed"
				}
				turn["error"] = Record{"code": code, "message": "Provider did not return a valid answer; retry or continue manually"}
			} else {
				turn["status"] = "completed"
				turn["answer"] = s.answerWithHypotheses(turn, answer)
			}
			s.state.add("notifications", str(turn["projectId"]), Record{"kind": "turn", "targetId": tid, "message": "Investigation " + str(turn["status"]), "read": false, "revision": 1})
		})
	}()
}
func sliceOf(v any) []any {
	switch a := v.(type) {
	case []any:
		return a
	case []Record:
		out := []any{}
		for _, r := range a {
			out = append(out, r)
		}
		return out
	}
	return []any{}
}
func earliestExpiry(snapshot Record, retention time.Duration) string {
	earliest := time.Now().UTC().Add(retention)
	for _, v := range sliceOf(snapshot["evidence"]) {
		if t, e := time.Parse(time.RFC3339Nano, str(record(v)["expiresAt"])); e == nil && t.Before(earliest) {
			earliest = t
		}
	}
	return earliest.Format(time.RFC3339Nano)
}

// redact recursively enforces expiry and independent source grants for aggregates,
// immutable snapshots, exports and idempotent replays as well as direct reads.
func (q *request) redact(v any) any {
	switch value := v.(type) {
	case string:
		return maskText(value)
	case Record:
		return q.redactRecord(value)
	case map[string]any:
		return q.redactRecord(Record(value))
	case []Record:
		out := []any{}
		for _, r := range value {
			out = append(out, q.redactRecord(r))
		}
		return out
	case []any:
		out := []any{}
		for _, r := range value {
			out = append(out, q.redact(r))
		}
		return out
	}
	if v != nil {
		kind := reflect.ValueOf(v).Kind()
		if kind == reflect.Struct || kind == reflect.Slice || kind == reflect.Ptr {
			raw, err := json.Marshal(v)
			if err == nil {
				var normalized any
				if json.Unmarshal(raw, &normalized) == nil {
					return q.redact(normalized)
				}
			}
		}
	}
	return v
}
func (q *request) redactRecord(value Record) Record {
	r := clone(value)
	if rid := str(r["id"]); rid != "" {
		for _, bucket := range q.s.state.Objects {
			if current := bucket[rid]; current != nil && q.expired(current) {
				r["expiresAt"] = current["expiresAt"]
				break
			}
		}
	}
	expired := q.expired(r) || (dependentBody(r) && q.hasExpiredRefs(r, map[string]bool{}))
	sourceDenied := q.hasSourceRefs(r, map[string]bool{}) && q.permission("source") != nil
	if expired || sourceDenied {
		for _, k := range []string{"content", "fields", "answer", "summary", "observed", "expected", "method", "text", "reason", "before", "after", "question", "download", "targetValue", "bindingConfirmed"} {
			if _, ok := r[k]; ok {
				r[k] = nil
			}
		}
		if expired {
			r["availability"] = "expired"
		} else {
			r["availability"] = "restricted"
		}
	}
	for k, v := range r {
		if sensitiveField(k) {
			r[k] = "[REDACTED]"
		} else {
			r[k] = q.redact(v)
		}
	}
	return r
}

func (q *request) modelReady() bool {
	for _, p := range q.s.cfg.Projects {
		if p.ID == q.project {
			return p.ModelEnabled && q.s.cfg.Model.Ready()
		}
	}
	return false
}

// external releases the store lock during bounded read-only connector I/O.
// A mutating request must restart if any committed state changed while unlocked;
// it never rolls that concurrent commit back or publishes from a stale read.
func (q *request) external(fn func() error) *apiError {
	epoch := q.s.epoch
	q.s.mu.Unlock()
	err := fn()
	q.s.mu.Lock()
	if q.s.closed || q.s.unhealthy != nil {
		q.staleExternal = true
		return fail(503, "STORE_UNAVAILABLE", "Store closed during connector request")
	}
	if epoch != q.s.epoch && q.r.Method != "GET" {
		q.staleExternal = true
		return fail(409, "REVISION_CONFLICT", "State changed during connector lookup; retry this operation")
	}
	if err != nil {
		return connectorError(err)
	}
	return nil
}

func sourceDependency(v any) bool {
	switch r := v.(type) {
	case Record:
		if r["kind"] == "source" || r["repoId"] != nil {
			return true
		}
		for _, value := range r {
			if sourceDependency(value) {
				return true
			}
		}
	case map[string]any:
		return sourceDependency(Record(r))
	case []any:
		for _, x := range r {
			if sourceDependency(x) {
				return true
			}
		}
	case []Record:
		for _, x := range r {
			if sourceDependency(x) {
				return true
			}
		}
	}
	return false
}
func (q *request) snapshotUnavailable(snapshot Record) bool {
	if q.expired(snapshot) {
		return true
	}
	for _, v := range sliceOf(snapshot["evidence"]) {
		ev := record(v)
		if q.expired(ev) {
			return true
		}
		current, e := q.get("evidence", str(ev["id"]))
		if e != nil || q.expired(current) {
			return true
		}
	}
	return false
}

func evidenceStatus(ev Record) string {
	if ev["kind"] == "manual" {
		return "manual"
	}
	if ev["kind"] == "source" {
		if ev["verified"] == true {
			return "matched"
		}
		return "candidate"
	}
	return "unknown"
}
func (s *Server) validateTurn(turn Record, authHash string) *apiError {
	var actor *Token
	for i := range s.cfg.Tokens {
		token := &s.cfg.Tokens[i]
		if digest([]byte(token.Token)) == authHash && token.Actor == turn["actor"] {
			actor = token
			break
		}
	}
	if actor == nil {
		return fail(403, "PERMISSION_REVOKED", "Model sending authorization was revoked")
	}
	project := str(turn["projectId"])
	role := actor.Projects[project]
	q := request{s: s, project: project, role: role, actor: actor}
	if role == "" || role == "viewer" || q.permission("model") != nil || !q.modelReady() {
		return fail(403, "PERMISSION_REVOKED", "Project model authorization is unavailable")
	}
	if q.hasExpiredRefs(turn, map[string]bool{}) || q.snapshotUnavailable(record(turn["snapshot"])) {
		return fail(410, "SOURCE_EXPIRED", "Snapshot evidence expired before the turn could finish")
	}
	if q.hasSourceRefs(turn, map[string]bool{}) && q.permission("source") != nil {
		return fail(403, "PERMISSION_REVOKED", "Source sending permission was revoked")
	}
	if pid := str(turn["parentTurnId"]); pid != "" {
		parent, e := q.get("turns", pid)
		if e != nil || q.hasExpiredRefs(parent, map[string]bool{}) || q.snapshotUnavailable(record(parent["snapshot"])) {
			return fail(410, "SOURCE_EXPIRED", "Parent turn evidence expired")
		}
		if q.hasSourceRefs(parent, map[string]bool{}) && q.permission("source") != nil {
			return fail(403, "PERMISSION_REVOKED", "Parent turn source permission was revoked")
		}
	}
	return nil
}

func (q *request) modelEvidence(ev Record) connectors.Evidence {
	origin := record(maskValue(ev["originRef"]))
	content := maskText(str(ev["content"]))
	e := connectors.Evidence{ID: str(ev["id"]), Kind: str(ev["kind"]), Label: maskText(str(ev["label"])), Content: content, Verified: ev["verified"] == true, Availability: str(ev["availability"]), Status: evidenceStatus(ev), OriginRef: map[string]any(origin)}
	if ev["kind"] == "source" {
		e.Provenance = &connectors.SourceProvenance{MappingID: str(origin["mappingId"]), RepoID: str(origin["repoId"]), App: str(origin["app"]), Platform: str(origin["platform"]), Build: str(origin["build"]), Version: str(origin["version"]), Commit: str(origin["commit"]), Path: str(origin["path"]), Start: num(origin["start"]), End: num(origin["end"]), ContentHash: digest([]byte(content)), BlobHash: str(origin["blobHash"]), EventID: str(origin["eventId"]), AssetID: str(origin["assetId"]), IdentitySource: str(origin["identitySource"])}
	}
	return e
}

func (q *request) hasSourceRefs(value any, seen map[string]bool) bool {
	return q.walkDependencies(value, seen, false)
}
func (q *request) hasExpiredRefs(value any, seen map[string]bool) bool {
	return q.walkDependencies(value, seen, true)
}
func (q *request) walkDependencies(value any, seen map[string]bool, expiry bool) bool {
	switch r := value.(type) {
	case map[string]any:
		return q.walkDependencies(Record(r), seen, expiry)
	case Record:
		if expiry {
			if q.expired(r) {
				return true
			}
			if rid := str(r["id"]); rid != "" {
				for _, bucket := range q.s.state.Objects {
					if current := bucket[rid]; current != nil && q.expired(current) {
						return true
					}
				}
			}
		} else if r["kind"] == "source" || r["repoId"] != nil {
			return true
		}
		refs := map[string][]string{"evidence": append(stringsOf(r["evidenceIds"]), stringsOf(r["citations"])...), "turns": {str(r["turnId"]), str(r["parentTurnId"])}, "hypotheses": {str(r["hypothesisId"])}, "verifications": append(stringsOf(r["verificationIds"]), str(r["verificationId"])), "snapshots": {str(r["snapshotId"])}}
		for bucket, ids := range refs {
			for _, rid := range ids {
				if rid == "" {
					continue
				}
				key := bucket + ":" + rid
				if seen[key] {
					continue
				}
				seen[key] = true
				current := q.s.state.bucket(bucket)[rid]
				if current == nil || current["projectId"] != q.project {
					if expiry {
						return true
					}
					continue
				}
				if q.walkDependencies(current, seen, expiry) {
					return true
				}
			}
		}
		for _, v := range r {
			if q.walkDependencies(v, seen, expiry) {
				return true
			}
		}
	case []Record:
		for _, v := range r {
			if q.walkDependencies(v, seen, expiry) {
				return true
			}
		}
	case []any:
		for _, v := range r {
			if q.walkDependencies(v, seen, expiry) {
				return true
			}
		}
	}
	return false
}
func dependentBody(r Record) bool {
	_, hasAnswer := r["answer"]
	return hasAnswer || r["summary"] != nil || r["purpose"] != nil || r["kind"] == "verification" || r["kind"] == "source" || r["text"] != nil || r["snapshot"] != nil || r["evidence"] != nil
}
func (q *request) dependencyExpiry(value any) string {
	earliest := time.Now().UTC().Add(q.s.cfg.Retention)
	seen := map[string]bool{}
	var visit func(any)
	visit = func(value any) {
		switch r := value.(type) {
		case map[string]any:
			visit(Record(r))
		case Record:
			if t, e := time.Parse(time.RFC3339Nano, str(r["expiresAt"])); e == nil && t.Before(earliest) {
				earliest = t
			}
			refs := map[string][]string{"evidence": append(stringsOf(r["evidenceIds"]), stringsOf(r["citations"])...), "turns": {str(r["turnId"]), str(r["parentTurnId"])}, "hypotheses": {str(r["hypothesisId"])}, "verifications": append(stringsOf(r["verificationIds"]), str(r["verificationId"]))}
			for bucket, ids := range refs {
				for _, rid := range ids {
					key := bucket + ":" + rid
					if rid != "" && !seen[key] {
						seen[key] = true
						if current := q.s.state.bucket(bucket)[rid]; current != nil && current["projectId"] == q.project {
							visit(current)
						}
					}
				}
			}
			for _, v := range r {
				visit(v)
			}
		case []any:
			for _, v := range r {
				visit(v)
			}
		case []Record:
			for _, v := range r {
				visit(v)
			}
		}
	}
	visit(value)
	return earliest.Format(time.RFC3339Nano)
}
