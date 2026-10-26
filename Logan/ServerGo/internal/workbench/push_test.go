package workbench

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logan/servergo/internal/workbench/connectors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

const pushTestSecret = "callback-secret-01234567890123456789"

func pushCallback(s *Server, event Record, stamp string, valid bool) int {
	b, _ := json.Marshal(event)
	r := httptest.NewRequest("POST", connectors.PushCallbackPath, bytes.NewReader(b))
	r.Header.Set("X-Logan-Push-Timestamp", stamp)
	m := hmac.New(sha256.New, []byte(pushTestSecret))
	m.Write([]byte(stamp + "\n"))
	m.Write(b)
	sig := hex.EncodeToString(m.Sum(nil))
	if !valid {
		sig = "invalid"
	}
	r.Header.Set("X-Logan-Push-Signature", sig)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}
func pushDraft(t *testing.T, s *Server, key string) Record {
	_, out := req(t, s, "POST", "/collection-drafts", "p", key, "", Record{"targetValue": "explicit-synthetic-target", "bindingConfirmed": true, "title": "Debug test", "begin": "2026-10-26T00:00:00Z", "end": "2026-10-26T00:01:00Z"})
	return Record(data(t, out))
}
func TestPushCallbackBeforeResponseDurableAndAuthenticated(t *testing.T) {
	var s *Server
	sent := make(chan Record, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Record
		_ = json.NewDecoder(r.Body).Decode(&p)
		if r.Header.Get("Idempotency-Key") != p["attemptId"] || r.Header.Get("Authorization") != "Bearer outbound-token-01234567890123456789" {
			t.Error("outbound auth/idempotency")
		}
		sent <- p
		<-release
		w.WriteHeader(202)
		fmt.Fprint(w, `{"schemaVersion":1,"status":"accepted","providerRequestId":"job1"}`)
	}))
	defer upstream.Close()
	s = openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: upstream.URL, Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	d := pushDraft(t, s, "draft1")
	code, out := req(t, s, "POST", "/collections", "p", "send1", "", Record{"draftId": d["id"], "draftRevision": d["revision"]})
	if code != 201 {
		t.Fatal(code, out)
	}
	c := data(t, out)
	var payload Record
	select {
	case payload = <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("no HTTP send")
	}
	if record(payload["target"])["value"] != "explicit-synthetic-target" {
		t.Fatal(payload)
	}
	// The blocked upstream cannot hold the store lock.
	if code, _ := req(t, s, "GET", "/workspace", "p", "", "", nil); code != 200 {
		t.Fatal("locked")
	}
	event := Record{"schemaVersion": 1, "eventId": "evt1", "requestId": c["id"], "attemptId": c["currentAttemptId"], "providerRequestId": "job1", "eventType": "push_succeeded", "occurredAt": now()}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	if pushCallback(s, event, stamp, false) != 401 || pushCallback(s, event, "1", true) != 401 {
		t.Fatal("forged/stale accepted")
	}
	if pushCallback(s, event, stamp, true) != 200 || pushCallback(s, event, stamp, true) != 200 {
		t.Fatal("callback/duplicate")
	}
	event["reasonCode"] = "DIFFERENT"
	if pushCallback(s, event, stamp, true) != 409 {
		t.Fatal("conflicting event accepted")
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	_, out = req(t, s, "GET", "/collections/"+str(c["id"]), "p", "", "", nil)
	if data(t, out)["status"] != "waiting_logs" {
		t.Fatal("response regressed callback", out)
	}
	_, out = req(t, s, "GET", "/collections/"+str(c["id"])+"/events", "p", "", "", nil)
	if len(out["data"].([]any)) != 1 {
		t.Fatal("duplicate persisted")
	}
	dir := s.cfg.Dir
	_ = s.Close()
	s = openTest(t, dir)
	defer s.Close()
	_, out = req(t, s, "GET", "/collections/"+str(c["id"]), "p", "", "", nil)
	if data(t, out)["status"] != "waiting_logs" {
		t.Fatal("lost durable callback")
	}
}
func TestPushUnknownRetryCancelLateAndDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: upstream.URL, Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	d := pushDraft(t, s, "draft2")
	_, o := req(t, s, "POST", "/collections", "p", "send2", "", Record{"draftId": d["id"], "draftRevision": 1})
	c := data(t, o)
	cid := str(c["id"])
	aid := str(c["currentAttemptId"])
	for i := 0; i < 100; i++ {
		_, o = req(t, s, "GET", "/collections/"+cid, "p", "", "", nil)
		c = data(t, o)
		if c["status"] == "dispatch_unknown" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c["status"] != "dispatch_unknown" {
		t.Fatal(c)
	}
	code, o := req(t, s, "POST", "/collections/"+cid+"/retries", "p", "retry2", fmt.Sprint(c["revision"]), Record{"confirmed": true, "reason": "Old send could still reach client"})
	if code != 201 {
		t.Fatal(code, o)
	}
	c = data(t, o)
	code, o = req(t, s, "POST", "/collections/"+cid+"/cancel", "p", "cancel2", fmt.Sprint(c["revision"]), Record{"confirmed": true, "reason": "Stop tracking"})
	if code != 200 {
		t.Fatal(code, o)
	}
	ev := Record{"schemaVersion": 1, "eventId": "old", "requestId": cid, "attemptId": aid, "providerRequestId": "old-job", "eventType": "push_succeeded", "occurredAt": now()}
	if pushCallback(s, ev, strconv.FormatInt(time.Now().Unix(), 10), true) != 200 {
		t.Fatal("late observation rejected")
	}
	_, o = req(t, s, "GET", "/collections/"+cid, "p", "", "", nil)
	if data(t, o)["status"] != "cancelled" {
		t.Fatal("late callback changed cancelled")
	}
	s.mu.Lock()
	c2 := s.state.add("collections", "p", Record{"status": "waiting_push", "deadlineAt": time.Now().Add(-time.Second).Format(time.RFC3339Nano), "revision": 1})
	id2 := str(c2["id"])
	s.mu.Unlock()
	time.Sleep(1200 * time.Millisecond)
	_, o = req(t, s, "GET", "/collections/"+id2, "p", "", "", nil)
	if data(t, o)["status"] != "timed_out" {
		t.Fatal(o)
	}
}
func TestPushRestartInterruptsWithoutReplay(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	s.mu.Lock()
	c := s.state.add("collections", "p", Record{"status": "pending_dispatch", "revision": 1})
	a := s.state.add("pushAttempts", "p", Record{"collectionId": c["id"], "dispatchStatus": "sending", "pushStatus": "unknown"})
	c["currentAttemptId"] = a["id"]
	cid := str(c["id"])
	if e := s.store.Save(s.state); e != nil {
		t.Fatal(e)
	}
	s.mu.Unlock()
	s.Close()
	s = openTest(t, dir)
	defer s.Close()
	_, o := req(t, s, "GET", "/collections/"+cid, "p", "", "", nil)
	if data(t, o)["status"] != "dispatch_unknown" {
		t.Fatal("restart replayed or lost uncertain dispatch", o)
	}
}
func TestPushCallbackCommitFailureDoesNotAcknowledge(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: "http://127.0.0.1:1/jobs", Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	s.mu.Lock()
	c := s.state.add("collections", "p", Record{"status": "waiting_push", "revision": 1})
	a := s.state.add("pushAttempts", "p", Record{"collectionId": c["id"], "dispatchStatus": "accepted", "pushStatus": "unknown", "deadlineAt": time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
	c["currentAttemptId"] = a["id"]
	s.store = &failingPersistence{Persistence: s.store, fail: true}
	s.mu.Unlock()
	ev := Record{"schemaVersion": 1, "eventId": "fail-commit", "requestId": c["id"], "attemptId": a["id"], "providerRequestId": "job", "eventType": "push_succeeded", "occurredAt": now()}
	if pushCallback(s, ev, strconv.FormatInt(time.Now().Unix(), 10), true) != 503 {
		t.Fatal("acknowledged failed commit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.bucket("pushEvents")) != 0 || s.state.bucket("collections")[str(c["id"])]["status"] != "waiting_push" {
		t.Fatal("partial callback commit")
	}
}
func TestPushDeviceAllowsOptionalPurposeButAliasRequiresBinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer upstream.Close()
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: upstream.URL, Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	for i, kind := range []string{"device", "alias", "account"} {
		_, o := req(t, s, "POST", "/collection-drafts", "p", fmt.Sprint("minimal-draft", i), "", Record{"targetType": kind, "targetValue": "explicit-target", "begin": "2026-10-26T00:00:00Z", "end": "2026-10-26T00:01:00Z"})
		d := data(t, o)
		code, o := req(t, s, "POST", "/collections", "p", fmt.Sprint("minimal-send", i), "", Record{"draftId": d["id"], "draftRevision": 1})
		if kind == "device" && code != 201 {
			t.Fatal(code, o)
		}
		if kind != "device" && code != 422 {
			t.Fatal("unconfirmed binding accepted", kind, code, o)
		}
	}
}
func TestPushOptionalWindowAndUnknownDuplicateGuard(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: upstream.URL, Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	_, o := req(t, s, "POST", "/collection-drafts", "p", "blank-window", "", Record{"targetValue": "windowless-target"})
	d := data(t, o)
	code, o := req(t, s, "POST", "/collections", "p", "blank-window-send", "", Record{"draftId": d["id"], "draftRevision": 1})
	if code != 201 {
		t.Fatal("blank window rejected", code, o)
	}
	cid := str(data(t, o)["id"])
	for i := 0; i < 100; i++ {
		_, o = req(t, s, "GET", "/collections/"+cid, "p", "", "", nil)
		if data(t, o)["status"] == "dispatch_unknown" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, o = req(t, s, "POST", "/collections", "p", "bypass-uncertainty", "", Record{"draftId": d["id"], "draftRevision": 1})
	if code != 409 {
		t.Fatal("new request bypassed retry confirmation", code, o)
	}
}
func TestPushConcurrencyBoundsCancelAndShutdown(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Header.Get("Idempotency-Key")
		<-release
		w.WriteHeader(503)
	}))
	defer upstream.Close()
	defer close(release)
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: upstream.URL, Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	collections := []Record{}
	for i := 0; i < 4; i++ {
		_, o := req(t, s, "POST", "/collection-drafts", "p", fmt.Sprint("bounded-draft", i), "", Record{"targetValue": fmt.Sprint("bounded-target", i)})
		d := data(t, o)
		code, o := req(t, s, "POST", "/collections", "p", fmt.Sprint("bounded-send", i), "", Record{"draftId": d["id"], "draftRevision": 1})
		if code != 201 {
			t.Fatal(code, o)
		}
		collections = append(collections, Record(data(t, o)))
		if i < 2 {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("expected initial send")
			}
		}
	}
	select {
	case <-entered:
		t.Fatal("more than two outbound calls")
	case <-time.After(100 * time.Millisecond):
	}
	third := collections[2]
	code, o := req(t, s, "POST", "/collections/"+str(third["id"])+"/cancel", "p", "bounded-cancel", "1", Record{"confirmed": true, "reason": "Stop queued send"})
	if code != 200 {
		t.Fatal(code, o)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
		t.Fatal("cancelled or shutdown waiter called provider")
	default:
	}
}
func TestPushRetentionAndIdleSweeper(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.mu.Lock()
	before := s.state
	s.mu.Unlock()
	time.Sleep(1100 * time.Millisecond)
	s.mu.Lock()
	if s.state != before {
		t.Fatal("idle sweeper cloned entire state")
	}
	s.cfg.Push, _ = connectors.NewPush(connectors.PushConfig{Endpoint: "http://127.0.0.1:1/jobs", Token: "outbound-token-01234567890123456789", CallbackSecret: pushTestSecret, CallbackURL: "http://localhost" + connectors.PushCallbackPath, Projects: []string{"p"}})
	expires := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	c := s.state.add("collections", "p", Record{"status": "dispatch_unknown", "revision": 1, "targetValue": "retained-routing-target", "expiresAt": expires})
	a := s.state.add("pushAttempts", "p", Record{"collectionId": c["id"], "dispatchStatus": "unknown", "pushStatus": "unknown", "deadlineAt": time.Now().Add(time.Minute).Format(time.RFC3339Nano), "reason": "sensitive-purpose", "expiresAt": expires})
	c["currentAttemptId"] = a["id"]
	cid := str(c["id"])
	aid := str(a["id"])
	s.purgeExpired()
	raw, _ := json.Marshal(s.state)
	s.mu.Unlock()
	if bytes.Contains(raw, []byte("retained-routing-target")) || bytes.Contains(raw, []byte("sensitive-purpose")) {
		t.Fatal("expired collection content retained")
	}
	code, o := req(t, s, "POST", "/collections/"+cid+"/retries", "p", "expired-retry", "1", Record{"confirmed": true, "reason": "retry"})
	if code != 410 {
		t.Fatal("expired retry allowed", code, o)
	}
	ev := Record{"schemaVersion": 1, "eventId": "expired-event", "requestId": cid, "attemptId": aid, "providerRequestId": "job", "eventType": "push_succeeded", "occurredAt": now()}
	if pushCallback(s, ev, strconv.FormatInt(time.Now().Unix(), 10), true) != 200 {
		t.Fatal("expired observation not retained")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.bucket("collections")[cid]["status"] != "dispatch_unknown" {
		t.Fatal("expired callback advanced collection")
	}
	for _, event := range s.state.bucket("pushEvents") {
		if event["outcome"] != "ignored" || event["expiresAt"] != expires || str(event["receivedAt"]) == "" {
			t.Fatal("missing expiry/receipt/ignored observation", event)
		}
	}
}
