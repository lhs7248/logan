package workbench

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (q *request) createCollection(b Record) (any, int, *apiError) {
	if !q.s.cfg.Push.Ready(q.project) {
		return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Internal push is not configured for this project")
	}
	if len(b) != 2 {
		return nil, 0, fail(422, "COLLECTION_INVALID", "Use draftId and draftRevision")
	}
	d, e := q.get("collectionDrafts", str(b["draftId"]))
	if e != nil {
		return nil, 0, e
	}
	if d["actor"] != q.actor.Actor {
		return nil, 0, fail(404, "NOT_FOUND", "Draft not found")
	}
	if q.expired(d) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Draft expired")
	}
	if num(d["revision"]) != num(b["draftRevision"]) {
		return nil, 0, fail(409, "REVISION_CONFLICT", "Draft changed")
	}
	if d["app"] != "com.hxl.xlb.test" || !contains([]string{"device", "account", "alias"}, str(d["targetType"])) || strings.TrimSpace(str(d["targetValue"])) == "" || strings.Contains(strings.ToUpper(str(d["targetValue"])), "[REDACTED") || (d["targetType"] != "device" && d["bindingConfirmed"] != true) {
		return nil, 0, fail(422, "COLLECTION_INVALID", "Actual target and account/alias binding confirmation required")
	}
	a, ea := time.Parse(time.RFC3339Nano, str(d["begin"]))
	z, ez := time.Parse(time.RFC3339Nano, str(d["end"]))
	if (str(d["begin"]) != "" || str(d["end"]) != "") && (ea != nil || ez != nil || !z.After(a)) {
		return nil, 0, fail(422, "COLLECTION_INVALID", "Valid complete time window required")
	}
	if str(d["issueId"]) != "" {
		issue, e := q.get("issues", str(d["issueId"]))
		if e != nil {
			return nil, 0, e
		}
		if q.expired(issue) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Issue expired")
		}
	}
	for _, c := range q.list("collections") {
		if (pushActive(c) || c["status"] == "dispatch_unknown") && c["app"] == d["app"] && c["targetType"] == d["targetType"] && c["targetValue"] == d["targetValue"] && c["begin"] == d["begin"] && c["end"] == d["end"] {
			return nil, 0, fail(409, "COLLECTION_ACTIVE", "Identical collection is already active")
		}
	}
	c := Record{"revision": 1, "actor": q.actor.Actor, "draftId": d["id"], "expiresAt": time.Now().Add(q.s.cfg.Retention).UTC().Format(time.RFC3339Nano)}
	for _, k := range []string{"title", "issueId", "app", "targetType", "targetValue", "begin", "end"} {
		c[k] = d[k]
	}
	c = q.s.state.add("collections", q.project, c)
	q.newPushAttempt(c, "", 1)
	return c, 201, nil
}
func pushActive(c Record) bool {
	return contains([]string{"pending_dispatch", "waiting_push", "waiting_logs"}, str(c["status"]))
}
func (q *request) newPushAttempt(c Record, reason string, number int) {
	deadline := time.Now().Add(q.s.cfg.Push.Deadline()).UTC().Format(time.RFC3339Nano)
	a := q.s.state.add("pushAttempts", q.project, Record{"collectionId": c["id"], "number": number, "dispatchStatus": "queued", "pushStatus": "unknown", "providerRequestId": "", "deadlineAt": deadline, "reason": reason, "expiresAt": c["expiresAt"]})
	c["currentAttemptId"] = a["id"]
	c["deadlineAt"] = deadline
	c["status"] = "pending_dispatch"
	aid := str(a["id"])
	q.afterCommit = append(q.afterCommit, func() { q.s.jobs.Add(1); go func() { defer q.s.jobs.Done(); q.s.dispatchPush(aid) }() })
}
func (q *request) collectionRoute(parts []string, b Record) (any, int, *apiError) {
	c, e := q.get("collections", parts[1])
	if e != nil {
		return nil, 0, e
	}
	if len(parts) == 2 && q.r.Method == "GET" {
		return c, 200, nil
	}
	if len(parts) != 3 {
		return nil, 0, fail(404, "NOT_FOUND", "Unknown collection route")
	}
	if q.r.Method == "GET" {
		kind := map[string]string{"attempts": "pushAttempts", "events": "pushEvents"}[parts[2]]
		if kind != "" {
			out := []Record{}
			for _, v := range q.list(kind) {
				if v["collectionId"] == c["id"] {
					delete(v, "bodyHash")
					out = append(out, v)
				}
			}
			return out, 200, nil
		}
	}
	if q.r.Method != "POST" || !contains([]string{"retries", "cancel"}, parts[2]) {
		return nil, 0, fail(404, "NOT_FOUND", "Unknown collection route")
	}
	if q.expired(c) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Collection expired; create a fresh draft")
	}
	if e := q.match(c, "revision"); e != nil {
		return nil, 0, e
	}
	reason := strings.TrimSpace(str(b["reason"]))
	if b["confirmed"] != true || reason == "" || len(reason) > 512 || len(b) != 2 {
		return nil, 0, fail(422, "CONFIRMATION_REQUIRED", "Explicit confirmation and bounded reason required")
	}
	if parts[2] == "cancel" {
		c["status"] = "cancelled"
		c["revision"] = num(c["revision"]) + 1
		q.s.state.add("pushEvents", q.project, Record{"collectionId": c["id"], "attemptId": c["currentAttemptId"], "eventType": "cancelled", "reason": reason, "outcome": "applied", "receivedAt": now(), "expiresAt": c["expiresAt"]})
		return c, 200, nil
	}
	if !q.s.cfg.Push.Ready(q.project) {
		return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Internal push is not configured")
	}
	if pushActive(c) {
		return nil, 0, fail(409, "RETRY_INVALID", "Current attempt is still active")
	}
	old := q.s.state.bucket("pushAttempts")[str(c["currentAttemptId"])]
	n := num(old["number"]) + 1
	if n > 5 {
		return nil, 0, fail(409, "RETRY_LIMIT", "Maximum five attempts")
	}
	c["revision"] = num(c["revision"]) + 1
	q.newPushAttempt(c, reason, n)
	return c, 201, nil
}

// Caller holds mu; every background transition commits a cloned state or fences writes.
func (s *Server) pushCommit(change func() bool) bool {
	if s.closed || s.unhealthy != nil {
		return false
	}
	before := s.state
	s.state = clone(before)
	if !change() {
		s.state = before
		return false
	}
	raw, _ := json.Marshal(s.state)
	if int64(len(raw)) > s.cfg.MaxStateBytes {
		s.state = before
		return false
	}
	if e := s.store.Save(s.state); e != nil {
		s.state = before
		s.unhealthy = e
		return false
	}
	s.epoch++
	return true
}
func (s *Server) dispatchPush(aid string) {
	select {
	case s.pushSlots <- struct{}{}:
	case <-s.ctx.Done():
		return
	}
	defer func() { <-s.pushSlots }()
	s.mu.Lock()
	var payload Record
	ok := s.pushCommit(func() bool {
		a := s.state.bucket("pushAttempts")[aid]
		if a == nil || a["dispatchStatus"] != "queued" {
			return false
		}
		c := s.state.bucket("collections")[str(a["collectionId"])]
		deadline, _ := time.Parse(time.RFC3339Nano, str(a["deadlineAt"]))
		if c == nil || recordExpired(c) || !pushActive(c) || c["currentAttemptId"] != aid || !time.Now().Before(deadline) {
			return false
		}
		a["dispatchStatus"] = "sending"
		payload = Record{"schemaVersion": 1, "requestId": c["id"], "attemptId": aid, "projectId": c["projectId"], "app": c["app"], "target": Record{"type": c["targetType"], "value": c["targetValue"]}, "context": Record{"issueId": c["issueId"], "title": c["title"], "begin": c["begin"], "end": c["end"]}, "callbackURL": s.cfg.Push.CallbackURL(), "deadlineAt": a["deadlineAt"]}
		return true
	})
	s.mu.Unlock()
	if !ok {
		return
	}
	status, provider := s.cfg.Push.Dispatch(s.ctx, payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushCommit(func() bool {
		a := s.state.bucket("pushAttempts")[aid]
		c := s.state.bucket("collections")[str(a["collectionId"])]
		if a["pushStatus"] != "unknown" {
			return false
		}
		if str(a["providerRequestId"]) != "" && provider != "" && a["providerRequestId"] != provider {
			status = "unknown"
			provider = ""
		}
		a["dispatchStatus"] = status
		if provider != "" {
			a["providerRequestId"] = provider
		}
		if c["currentAttemptId"] == aid && pushActive(c) {
			c["revision"] = num(c["revision"]) + 1
			switch status {
			case "accepted":
				c["status"] = "waiting_push"
			case "rejected":
				c["status"] = "push_failed"
			default:
				c["status"] = "dispatch_unknown"
			}
		}
		return true
	})
}

var pushReason = regexp.MustCompile(`^[A-Z0-9_]{0,64}$`)

func (s *Server) servePushEvent(w http.ResponseWriter, r *http.Request, trace string) {
	reject := func(status int, code string) { writeError(w, trace, fail(status, code, "Push event rejected")) }
	if r.Method != "POST" {
		reject(405, "METHOD_NOT_ALLOWED")
		return
	}
	secret := s.cfg.Push.CallbackSecret()
	if secret == "" {
		reject(503, "CONNECTOR_UNCONFIGURED")
		return
	}
	stamp := r.Header.Get("X-Logan-Push-Timestamp")
	seconds, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || time.Since(time.Unix(seconds, 0)) > 300*time.Second || time.Until(time.Unix(seconds, 0)) > 300*time.Second {
		reject(401, "PUSH_AUTH_INVALID")
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	if e != nil || len(body) > 65536 {
		reject(413, "BODY_TOO_LARGE")
		return
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "\n"))
	mac.Write(body)
	signature := r.Header.Get("X-Logan-Push-Signature")
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		reject(401, "PUSH_AUTH_INVALID")
		return
	}
	var event struct {
		SchemaVersion     int    `json:"schemaVersion"`
		EventID           string `json:"eventId"`
		RequestID         string `json:"requestId"`
		AttemptID         string `json:"attemptId"`
		ProviderRequestID string `json:"providerRequestId"`
		EventType         string `json:"eventType"`
		OccurredAt        string `json:"occurredAt"`
		ReasonCode        string `json:"reasonCode"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if dec.Decode(&event) != nil || dec.Decode(new(any)) != io.EOF || event.SchemaVersion != 1 || event.EventID == "" || len(event.EventID) > 128 || len(event.ProviderRequestID) > 128 || strings.TrimSpace(event.ProviderRequestID) == "" || !contains([]string{"push_succeeded", "push_failed"}, event.EventType) || !pushReason.MatchString(event.ReasonCode) {
		reject(422, "PUSH_EVENT_INVALID")
		return
	}
	if _, e := time.Parse(time.RFC3339Nano, event.OccurredAt); e != nil {
		reject(422, "PUSH_EVENT_INVALID")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.unhealthy != nil {
		reject(503, "STORE_UNAVAILABLE")
		return
	}
	hash := digest(body)
	for _, v := range s.state.bucket("pushEvents") {
		if v["eventId"] == event.EventID {
			if v["bodyHash"] != hash {
				reject(409, "PUSH_EVENT_CONFLICT")
				return
			}
			w.WriteHeader(200)
			return
		}
	}
	a := s.state.bucket("pushAttempts")[event.AttemptID]
	c := s.state.bucket("collections")[event.RequestID]
	if a == nil || c == nil || a["collectionId"] != event.RequestID || a["projectId"] != c["projectId"] || !s.cfg.Push.Ready(str(c["projectId"])) {
		reject(404, "PUSH_ATTEMPT_UNKNOWN")
		return
	}
	if str(a["providerRequestId"]) != "" && a["providerRequestId"] != event.ProviderRequestID {
		reject(409, "PUSH_PROVIDER_CONFLICT")
		return
	}
	ok := s.pushCommit(func() bool {
		a = s.state.bucket("pushAttempts")[event.AttemptID]
		c = s.state.bucket("collections")[event.RequestID]
		outcome := "ignored"
		deadline, _ := time.Parse(time.RFC3339Nano, str(a["deadlineAt"]))
		if c["currentAttemptId"] == event.AttemptID && c["status"] != "cancelled" && c["status"] != "timed_out" && time.Now().Before(deadline) && a["pushStatus"] == "unknown" && !recordExpired(c) {
			outcome = "applied"
			a["dispatchStatus"] = "accepted"
			a["pushStatus"] = strings.TrimPrefix(event.EventType, "push_")
			if event.EventType == "push_succeeded" {
				c["status"] = "waiting_logs"
			} else {
				c["status"] = "push_failed"
			}
			c["revision"] = num(c["revision"]) + 1
		}
		a["providerRequestId"] = event.ProviderRequestID
		s.state.add("pushEvents", str(c["projectId"]), Record{"collectionId": c["id"], "attemptId": a["id"], "eventId": event.EventID, "bodyHash": hash, "providerRequestId": event.ProviderRequestID, "eventType": event.EventType, "occurredAt": event.OccurredAt, "reasonCode": event.ReasonCode, "outcome": outcome, "receivedAt": now(), "expiresAt": c["expiresAt"]})
		return true
	})
	if !ok {
		reject(503, "STORE_UNAVAILABLE")
		return
	}
	w.WriteHeader(200)
}
func (s *Server) recoverPush() {
	for _, a := range s.state.bucket("pushAttempts") {
		if a["dispatchStatus"] == "queued" || a["dispatchStatus"] == "sending" {
			a["dispatchStatus"] = "unknown"
			c := s.state.bucket("collections")[str(a["collectionId"])]
			if c != nil && c["currentAttemptId"] == a["id"] && pushActive(c) {
				c["status"] = "dispatch_unknown"
				c["revision"] = num(c["revision"]) + 1
			}
		}
	}
}
func (s *Server) startPushSweeper() {
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				due := false
				for _, c := range s.state.bucket("collections") {
					deadline, e := time.Parse(time.RFC3339Nano, str(c["deadlineAt"]))
					if e == nil && !time.Now().Before(deadline) && (pushActive(c) || c["status"] == "dispatch_unknown") {
						due = true
						break
					}
				}
				if !due || s.closed || s.unhealthy != nil {
					s.mu.Unlock()
					continue
				}
				s.pushCommit(func() bool {
					changed := false
					count := 0
					for _, c := range s.state.bucket("collections") {
						if count >= 100 {
							break
						}
						deadline, e := time.Parse(time.RFC3339Nano, str(c["deadlineAt"]))
						if e == nil && !time.Now().Before(deadline) && (pushActive(c) || c["status"] == "dispatch_unknown") {
							c["status"] = "timed_out"
							c["revision"] = num(c["revision"]) + 1
							changed = true
							count++
						}
					}
					return changed
				})
				s.mu.Unlock()
			}
		}
	}()
}
