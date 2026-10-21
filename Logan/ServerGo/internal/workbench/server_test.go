package workbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T, dir string) *Server {
	t.Helper()
	s, err := Open(Config{Dir: dir, Projects: []Project{{ID: "p", Name: "Project"}, {ID: "other", Name: "Other"}, {ID: "demo", Name: "Demo", Demo: true}}, Tokens: []Token{{Token: "secret", Actor: "alice", Projects: map[string]string{"p": "reviewer", "demo": "reviewer"}, Permissions: []string{"raw", "export", "source", "model"}}, {Token: "reader", Actor: "bob", Projects: map[string]string{"p": "viewer"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func req(t *testing.T, s *Server, method, path, project, key, revision string, body any) (int, map[string]any) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, Base+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", project)
	r.Header.Set("Idempotency-Key", keyFor(key))
	if revision != "" {
		r.Header.Set("If-Match", revision)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func data(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	d, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %v", out)
	}
	return d
}
func TestDurabilityAuthorizationIdempotencyAndRevision(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	r := httptest.NewRequest("GET", Base+"/workspace", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauth %d", w.Code)
	}
	code, out := req(t, s, "POST", "/issues", "p", "create", "", map[string]any{"title": "Crash", "context": map[string]any{"build": "42"}})
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	id := data(t, out)["id"].(string)
	code, out2 := req(t, s, "POST", "/issues", "p", "create", "", map[string]any{"title": "Crash", "context": map[string]any{"build": "42"}})
	if code != 201 || data(t, out2)["id"] != id {
		t.Fatalf("idempotency %v", out2)
	}
	code, _ = req(t, s, "POST", "/issues", "p", "create", "", map[string]any{"title": "Different"})
	if code != 409 {
		t.Fatalf("reused key %d", code)
	}
	code, _ = req(t, s, "PATCH", "/issues/"+id, "p", "edit", "0", map[string]any{"title": "Changed"})
	if code != 409 {
		t.Fatalf("stale %d", code)
	}
	code, _ = req(t, s, "GET", "/issues/"+id, "other", "", "", nil)
	if code != 403 {
		t.Fatalf("project %d", code)
	}
	if _, err := Open(Config{Dir: dir}); err == nil {
		t.Fatal("second process/store must fail closed")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir)
	defer s.Close()
	code, out = req(t, s, "GET", "/issues/"+id, "p", "", "", nil)
	if code != 200 || data(t, out)["title"] != "Crash" {
		t.Fatalf("restart %d %v", code, out)
	}
}
func upload(t *testing.T, s *Server, project, filename, content string) string {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("metadata", `{"platform":"web"}`)
	p, _ := mw.CreateFormFile("files", filename)
	_, _ = io.WriteString(p, content)
	_ = mw.Close()
	r := httptest.NewRequest("POST", Base+"/imports", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", project)
	r.Header.Set("Idempotency-Key", keyFor("upload-"+filename))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	return data(t, out)["assets"].([]any)[0].(map[string]any)["id"].(string)
}
func awaitRun(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		_, o := req(t, s, "GET", "/parse-runs/"+id, "p", "", "", nil)
		d := data(t, o)
		if d["status"] != "queued" && d["status"] != "running" {
			return d
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("parse timed out")
	return nil
}
func TestParsingFiltersAtomicHistoryAndSnapshot(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	a := upload(t, s, "p", "records.jsonl", "{\"time\":\"2026-10-21T10:00:00Z\",\"level\":\"error\",\"content\":\"bad\",\"requestId\":\"r1\"}\nnot json\n{\"content\":\"unknown\"}\n")
	code, o := req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "strict", "", map[string]any{"format": "jsonl", "mode": "strict"})
	if code != 201 {
		t.Fatal(o)
	}
	strict := data(t, o)["id"].(string)
	if d := awaitRun(t, s, strict); d["status"] != "failed" || d["eventCount"] != float64(0) {
		t.Fatalf("strict %v", d)
	}
	_, o = req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "skip", "", map[string]any{"format": "jsonl", "mode": "skip_invalid_records"})
	run := data(t, o)["id"].(string)
	if d := awaitRun(t, s, run); d["status"] != "partial" || d["eventCount"] != float64(2) {
		t.Fatalf("skip %v", d)
	}
	_, o = req(t, s, "GET", "/parse-runs/"+run+"/events?level=error", "p", "", "", nil)
	events := data(t, o)
	if events["total"] != float64(1) {
		t.Fatal(events)
	}
	event := events["events"].([]any)[0].(map[string]any)
	_, h := req(t, s, "GET", "/parse-runs/"+run+"/histogram?level=error", "p", "", "", nil)
	if data(t, h)["filterHash"] != events["filterHash"] || data(t, h)["total"] != float64(1) {
		t.Fatal(h)
	}
	_, o = req(t, s, "POST", "/issues", "p", "issue", "", map[string]any{"title": "Crash"})
	issue := data(t, o)["id"].(string)
	_, o = req(t, s, "POST", "/issues/"+issue+"/evidence", "p", "evidence", "", map[string]any{"kind": "log", "originRef": map[string]any{"runId": run, "eventIds": []string{event["id"].(string)}}})
	e := data(t, o)["id"].(string)
	code, o = req(t, s, "POST", "/issues/"+issue+"/turns", "p", "turn", "", map[string]any{"question": "Why?", "contextRevision": 2})
	if code != 201 {
		t.Fatalf("turn %d %v", code, o)
	}
	turn := data(t, o)
	if turn["answer"] != nil {
		t.Fatal("unconfigured model fabricated answer")
	}
	snapshot := turn["snapshot"].(map[string]any)
	code, _ = req(t, s, "PATCH", "/issues/"+issue+"/context", "p", "remove", "2", map[string]any{"remove": []string{e}})
	if code != 200 {
		t.Fatalf("context %d", code)
	}
	_, o = req(t, s, "GET", "/evidence-snapshots/"+snapshot["id"].(string), "p", "", "", nil)
	if len(data(t, o)["evidenceIds"].([]any)) != 1 {
		t.Fatal("snapshot mutated")
	}
}
func TestHandoffRequiresFixVerificationAndRejectsStaleDraft(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", map[string]any{"title": "Crash"})
	id := data(t, o)["id"].(string)
	code, o := req(t, s, "POST", "/issues/"+id+"/handoff-previews", "p", "draft", "", map[string]any{"mode": "manual", "summary": "investigated", "status": "awaiting_verification", "issueRevision": 1, "contextRevision": 1})
	if code != 201 {
		t.Fatal(o)
	}
	d := data(t, o)
	code, o = req(t, s, "POST", "/issues/"+id+"/handoffs", "p", "save", "1", map[string]any{"draftId": d["id"], "version": d["version"], "hash": d["hash"], "confirmed": true})
	if code != 201 {
		t.Fatal(o)
	}
	code, _ = req(t, s, "PATCH", "/issues/"+id, "p", "resolve", "2", map[string]any{"status": "resolved"})
	if code != 422 {
		t.Fatalf("unverified resolve %d", code)
	}
	code, o = req(t, s, "POST", "/issues/"+id+"/handoff-previews", "p", "draft2", "", map[string]any{"mode": "manual", "summary": "old draft", "status": "awaiting_verification", "issueRevision": 2, "contextRevision": 1})
	if code != 201 {
		t.Fatal(o)
	}
	d = data(t, o)
	_, proof := req(t, s, "POST", "/issues/"+id+"/evidence", "p", "fix-proof", "", Record{"kind": "manual", "content": "Test result"})
	proofID := str(data(t, proof)["id"])
	code, _ = req(t, s, "POST", "/issues/"+id+"/verifications", "p", "verify", "", map[string]any{"purpose": "fix", "method": "reproduce", "expected": "no crash", "observed": "no crash", "result": "pass", "build": "43", "scenario": "restart", "evidenceIds": []string{proofID}})
	if code != 201 {
		t.Fatalf("verify %d", code)
	}
	code, _ = req(t, s, "POST", "/issues/"+id+"/handoffs", "p", "stale-save", "2", map[string]any{"draftId": d["id"], "version": d["version"], "hash": d["hash"], "confirmed": true})
	if code != 409 {
		t.Fatalf("stale draft %d", code)
	}
}

var _ http.Handler = (*Server)(nil)

func TestExpiredContentAndRevokedPermissionCannotReplay(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", map[string]any{"title": "Crash"})
	iid := data(t, o)["id"].(string)
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "manual", "", map[string]any{"kind": "manual", "content": "sensitive evidence"})
	eid := data(t, o)["id"].(string)
	s.mu.Lock()
	s.state.bucket("evidence")[eid]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	_, o = req(t, s, "GET", "/workspace", "p", "", "", nil)
	raw, _ := json.Marshal(o)
	if bytes.Contains(raw, []byte("sensitive evidence")) {
		t.Fatal("expired evidence leaked through workspace")
	}
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "manual", "", map[string]any{"kind": "manual", "content": "sensitive evidence"})
	raw, _ = json.Marshal(o)
	if bytes.Contains(raw, []byte("sensitive evidence")) {
		t.Fatal("expired evidence leaked through idempotency replay")
	}
}
func TestUUIDv7AndViewerMutation(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	r := httptest.NewRequest("POST", Base+"/issues", bytes.NewBufferString(`{"title":"Crash"}`))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "p")
	r.Header.Set("Idempotency-Key", "not-a-uuid")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("invalid UUID accepted: %d", w.Code)
	}
	r = httptest.NewRequest("POST", Base+"/issues", bytes.NewBufferString(`{"title":"Crash"}`))
	r.Header.Set("Authorization", "Bearer reader")
	r.Header.Set("X-Project-ID", "p")
	r.Header.Set("Idempotency-Key", keyFor("viewer"))
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

var testMillis = time.Now().UnixMilli()

func keyFor(key string) string {
	if key == "" {
		return ""
	}
	b := sha256.Sum256([]byte(key))
	for i := 5; i >= 0; i-- {
		b[i] = byte(testMillis >> uint((5-i)*8))
	}
	b[6] = (b[6] & 15) | 0x70
	b[8] = (b[8] & 63) | 0x80
	h := hex.EncodeToString(b[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}

func TestImportRecoveryAndOperationLookup(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	a := upload(t, s, "p", "recovery.txt", "one\ntwo")
	_, o := req(t, s, "GET", "/operations?key="+keyFor("upload-recovery.txt"), "p", "", "", nil)
	op := data(t, o)
	if op["status"] != "completed" || op["resourceId"] == nil {
		t.Fatalf("operation %v", o)
	}
	_, o = req(t, s, "GET", "/imports/"+op["resourceId"].(string), "p", "", "", nil)
	if data(t, o)["assets"].([]any)[0].(map[string]any)["id"] != a {
		t.Fatal("batch recovery lost asset")
	}
}
func TestLatestFixFailureBlocksResolution(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", map[string]any{"title": "Crash"})
	iid := data(t, o)["id"].(string)
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "proof", "", map[string]any{"kind": "manual", "content": "human observation"})
	eid := data(t, o)["id"].(string)
	for _, result := range []string{"pass", "fail"} {
		code, o := req(t, s, "POST", "/issues/"+iid+"/verifications", "p", "verify-"+result, "", map[string]any{"purpose": "fix", "method": "test", "expected": "no crash", "observed": result, "result": result, "build": "43", "scenario": "restart", "evidenceIds": []string{eid}})
		if code != 201 {
			t.Fatal(o)
		}
	}
	code, o := req(t, s, "PATCH", "/issues/"+iid, "p", "await", "1", map[string]any{"status": "awaiting_verification", "reason": "Prepare fix review"})
	if code != 200 {
		t.Fatal(o)
	}
	code, _ = req(t, s, "PATCH", "/issues/"+iid, "p", "resolve", "2", map[string]any{"status": "resolved"})
	if code != 422 {
		t.Fatalf("older pass overrode latest fail: %d", code)
	}
}
func TestIdempotencyNormalizesJSONAndMultipartBoundary(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	body := `{"title":"Canonical","context":{"build":"42"}}`
	r := httptest.NewRequest("POST", Base+"/issues", bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "p")
	r.Header.Set("Idempotency-Key", keyFor("canonical"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", Base+"/issues", bytes.NewBufferString("{ \"context\": {\"build\": \"42\"}, \"title\": \"Canonical\" }"))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "p")
	r.Header.Set("Idempotency-Key", keyFor("canonical"))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("equivalent JSON conflicted: %d %s", w.Code, w.Body.String())
	}
	a := upload(t, s, "p", "same.txt", "same content")
	if b := upload(t, s, "p", "same.txt", "same content"); b != a {
		t.Fatal("multipart replay created new asset")
	}
}

func TestExpiredDraftIsRejectedWithoutChangingIssue(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", map[string]any{"title": "Crash"})
	iid := data(t, o)["id"].(string)
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "proof", "", map[string]any{"kind": "manual", "content": "observation"})
	eid := data(t, o)["id"].(string)
	_, o = req(t, s, "POST", "/issues/"+iid+"/handoff-previews", "p", "draft", "", map[string]any{"mode": "manual", "summary": "Observation", "status": "investigating", "issueRevision": 1, "contextRevision": 2})
	d := data(t, o)
	s.mu.Lock()
	s.state.bucket("evidence")[eid]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	code, o := req(t, s, "POST", "/issues/"+iid+"/handoffs", "p", "save", "1", map[string]any{"draftId": d["id"], "version": d["version"], "hash": d["hash"], "confirmed": true})
	if code != 410 {
		t.Fatalf("expired draft accepted %d %v", code, o)
	}
}
func TestSourceDerivedTextRestrictedAcrossAllViews(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.mu.Lock()
	ev := s.state.add("evidence", "p", Record{"kind": "source", "content": "secret source", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	snapshot := Record{"evidence": []Record{clone(ev)}}
	turn := s.state.add("turns", "p", Record{"snapshot": snapshot, "status": "completed", "answer": Record{"summary": "secret source in answer"}})
	s.state.add("updates", "p", Record{"snapshot": snapshot, "summary": "secret source in handoff"})
	s.cfg.Tokens[0].Permissions = []string{"export", "model"}
	s.mu.Unlock()
	_, o := req(t, s, "GET", "/workspace", "p", "", "", nil)
	raw, _ := json.Marshal(o)
	if bytes.Contains(raw, []byte("secret source")) {
		t.Fatal("source-derived content leaked in workspace")
	}
	_, o = req(t, s, "GET", "/turns/"+str(turn["id"]), "p", "", "", nil)
	if data(t, o)["answer"] != nil {
		t.Fatal("source-derived answer leaked in direct read")
	}
}
func TestSensitivePresentationAndModelPreparation(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	a := upload(t, s, "p", "secret.jsonl", "{\"content\":\"Authorization: Bearer TOPSECRET password=TOPPASSWORD user@example.com\",\"token\":\"PRIVATE_TOKEN\",\"email\":\"person@example.com\"}\n")
	_, o := req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "parse", "", map[string]any{"format": "jsonl", "mode": "strict"})
	rid := data(t, o)["id"].(string)
	awaitRun(t, s, rid)
	_, o = req(t, s, "GET", "/parse-runs/"+rid+"/events", "p", "", "", nil)
	raw, _ := json.Marshal(o)
	for _, secret := range []string{"TOPSECRET", "TOPPASSWORD", "PRIVATE_TOKEN", "person@example.com", "user@example.com"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("presentation exposed %s", secret)
		}
	}
}
func TestStateQuotaRejectsBeforeCommit(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.cfg.MaxStateBytes = 512
	code, _ := req(t, s, "POST", "/issues", "p", "quota", "", map[string]any{"title": "Too big"})
	if code != 507 {
		t.Fatalf("quota %d", code)
	}
	_, o := req(t, s, "GET", "/issues", "p", "", "", nil)
	if len(o["data"].([]any)) != 0 {
		t.Fatal("over-quota mutation partially committed")
	}
}

func TestProtocolSignatureCannotFallBackToText(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	for n, content := range []string{`{"logArray":"%7B%22v%22%3A1%2C%22l%22%3A%22YQ%3D%3D%22%7D"}`, `%7B%22v%22%3A1%2C%22l%22%3A%22YQ%3D%3D%22%7D`, "%7B%22v%22%3A1%2C%22l%22%3A%22YQ%3D%3D%22%7D\n%7B%22v%22%3A1%2C%22l%22%3A%22YQ%3D%3D%22%7D", `[{"v":1,"l":"YQ=="}]`} {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		_ = mw.WriteField("metadata", `{"declaredFormat":"text"}`)
		p, _ := mw.CreateFormFile("files", "protocol.txt")
		_, _ = io.WriteString(p, content)
		_ = mw.Close()
		r := httptest.NewRequest("POST", Base+"/imports", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Project-ID", "p")
		r.Header.Set("Idempotency-Key", keyFor(fmt.Sprint("protocol", n)))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("protocol accepted as text: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestEditableDraftPersistsIncompleteTextAndConflicts(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", Record{"title": "Draft"})
	iid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/issues/"+iid+"/handoff-drafts", "p", "draft", "", Record{"summary": "unfinished ", "mode": "manual"})
	if code != 201 {
		t.Fatal(o)
	}
	did := str(data(t, o)["id"])
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir)
	defer s.Close()
	_, o = req(t, s, "GET", "/handoff-drafts/"+did, "p", "", "", nil)
	if data(t, o)["summary"] != "unfinished " {
		t.Fatal("draft lost on restart")
	}
	code, _ = req(t, s, "PATCH", "/handoff-drafts/"+did, "p", "stale", "0", Record{"summary": "clobber"})
	if code != 409 {
		t.Fatal("draft stale overwrite")
	}
	code, o = req(t, s, "PATCH", "/handoff-drafts/"+did, "p", "edit", "1", Record{"summary": "new text"})
	if code != 200 || num(data(t, o)["revision"]) != 2 {
		t.Fatal(o)
	}
}
func TestFixClosureRequiresFreshLifecycleProofAndServerTimestamps(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", Record{"title": "Lifecycle"})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "proof", "", Record{"kind": "manual", "content": "test execution screenshot description"})
	eid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/issues/"+iid+"/verifications", "p", "verify", "", Record{"purpose": "fix", "method": "reproduce", "expected": "no crash", "observed": "no crash", "result": "pass", "build": "43", "scenario": "restart", "evidenceIds": []string{eid}, "createdAt": "2999-01-01T00:00:00Z"})
	if code != 201 || data(t, o)["createdAt"] == "2999-01-01T00:00:00Z" {
		t.Fatal("client forged verification time")
	}
	for n, status := range []string{"awaiting_verification", "resolved", "reopened", "awaiting_verification"} {
		code, o = req(t, s, "PATCH", "/issues/"+iid, "p", fmt.Sprint("status", n), fmt.Sprint(n+1), Record{"status": status, "reason": "Human reviewed lifecycle"})
		if code != 200 {
			t.Fatal(o)
		}
	}
	code, _ = req(t, s, "PATCH", "/issues/"+iid, "p", "resolve-again", "5", Record{"status": "resolved", "reason": "Try old proof"})
	if code != 422 {
		t.Fatal("reopened issue reused old proof")
	}
	_, o = req(t, s, "GET", "/issues/"+iid+"/history", "p", "", "", nil)
	if len(o["data"].([]any)) < 5 {
		t.Fatal("status history missing")
	}
}

type failingPersistence struct {
	Persistence
	fail bool
}

func (f *failingPersistence) Save(s *State) error {
	if f.fail {
		return fmt.Errorf("injected storage fault")
	}
	return f.Persistence.Save(s)
}
func TestPersistentCommitFailureFailsClosedAndDoesNotPartiallyWrite(t *testing.T) {
	dir := t.TempDir()
	disk, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &failingPersistence{Persistence: disk}
	s, err := Open(Config{Store: store, Projects: []Project{{ID: "p"}}, Tokens: []Token{{Token: "secret", Actor: "alice", Projects: map[string]string{"p": "reviewer"}}}})
	if err != nil {
		t.Fatal(err)
	}
	store.fail = true
	code, _ := req(t, s, "POST", "/issues", "p", "failed", "", Record{"title": "Must not commit"})
	if code != 503 {
		t.Fatal(code)
	}
	code, _ = req(t, s, "GET", "/workspace", "p", "", "", nil)
	if code != 503 {
		t.Fatal("store failure did not fail closed")
	}
	_ = s.Close()
	s = openTest(t, dir)
	defer s.Close()
	_, o := req(t, s, "GET", "/issues", "p", "", "", nil)
	if len(o["data"].([]any)) != 0 {
		t.Fatal("failed write survived restart")
	}
}

func TestDerivedParentAndVerificationDependenciesRestrictAndExpire(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	s.mu.Lock()
	source := s.state.add("evidence", "p", Record{"kind": "source", "content": "PRIVATE_CODE", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	sid := str(source["id"])
	parent := s.state.add("turns", "p", Record{"status": "completed", "snapshot": Record{"evidenceIds": []string{sid}, "evidence": []Record{clone(source)}}, "answer": Record{"summary": "PRIVATE_CODE"}})
	child := s.state.add("turns", "p", Record{"status": "completed", "parentTurnId": parent["id"], "snapshot": Record{"evidenceIds": []string{}, "evidence": []Record{}}, "answer": Record{"summary": "PRIVATE_CODE copied from parent"}})
	draft := s.state.add("drafts", "p", Record{"mode": "ai", "turnId": parent["id"], "snapshot": Record{"evidenceIds": []string{}}, "summary": "PRIVATE_CODE draft"})
	s.state.add("verifications", "p", Record{"purpose": "fix", "evidenceIds": []string{sid}, "observed": "PRIVATE_CODE verification"})
	s.cfg.Tokens[0].Permissions = []string{"model", "export"}
	s.mu.Unlock()
	for _, path := range []string{"/workspace", "/turns/" + str(child["id"]), "/handoff-drafts/" + str(draft["id"])} {
		_, o := req(t, s, "GET", path, "p", "", "", nil)
		raw, _ := json.Marshal(o)
		if bytes.Contains(raw, []byte("PRIVATE_CODE")) {
			t.Fatalf("derived cache leak at %s", path)
		}
	}
	s.mu.Lock()
	s.cfg.Tokens[0].Permissions = []string{"source", "model", "export"}
	s.state.bucket("evidence")[sid]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.purgeExpired()
	state, _ := json.Marshal(s.state)
	s.mu.Unlock()
	if bytes.Contains(state, []byte("PRIVATE_CODE")) {
		t.Fatal("expired parent/verification derived content remained on disk state")
	}
}
func TestFractionalEventTimesSortChronologically(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	a := upload(t, s, "p", "fraction.jsonl", "{\"time\":\"2026-10-21T10:00:00.11Z\",\"content\":\"later\"}\n{\"time\":\"2026-10-21T10:00:00.1Z\",\"content\":\"earlier\"}\n")
	_, o := req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "fraction-parse", "", Record{})
	rid := str(data(t, o)["id"])
	awaitRun(t, s, rid)
	_, o = req(t, s, "GET", "/parse-runs/"+rid+"/events", "p", "", "", nil)
	if str(record(sliceOf(data(t, o)["events"])[0])["content"]) != "earlier" {
		t.Fatal("fractional timestamps sorted lexically")
	}
}

func TestLongLivedIssueCanHandoffAndExportAfterReplacingExpiredEvidence(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "issue", "", Record{"title": "Long investigation"})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "old-proof", "", Record{"kind": "manual", "content": "OLD_EXPIRED_OBSERVATION"})
	oldProof := str(data(t, o)["id"])
	verify := func(key, eid string) string {
		code, out := req(t, s, "POST", "/issues/"+iid+"/verifications", "p", key, "", Record{"purpose": "fix", "method": "manual test", "expected": "no crash", "observed": "no crash", "result": "pass", "build": "44", "scenario": "restart", "evidenceIds": []string{eid}})
		if code != 201 {
			t.Fatal(out)
		}
		return str(data(t, out)["id"])
	}
	oldVerification := verify("old-verification", oldProof)
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	oldIDs := stringsOf(data(t, o)["evidenceIds"])
	s.mu.Lock()
	past := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.state.bucket("evidence")[oldProof]["expiresAt"] = past
	s.state.bucket("verifications")[oldVerification]["expiresAt"] = past
	s.mu.Unlock()
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "new-proof", "", Record{"kind": "manual", "content": "Fresh independent test"})
	newProof := str(data(t, o)["id"])
	freshVerification := verify("fresh-verification", newProof)
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	issue := data(t, o)
	code, o := req(t, s, "PATCH", "/issues/"+iid+"/context", "p", "replace-context", fmt.Sprint(num(issue["contextRevision"])), Record{"remove": oldIDs})
	if code != 200 {
		t.Fatal(o)
	}
	issue = data(t, o)
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoff-previews", "p", "fresh-preview", "", Record{"mode": "manual", "summary": "Fresh independent fix verification", "status": "awaiting_verification", "issueRevision": issue["revision"], "contextRevision": issue["contextRevision"]})
	if code != 201 {
		t.Fatalf("expired historical verification poisoned new draft: %v", o)
	}
	draft := data(t, o)
	if ids := stringsOf(draft["verificationIds"]); len(ids) != 1 || ids[0] != freshVerification {
		t.Fatal("preview retained expired verification dependency")
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoffs", "p", "fresh-handoff", fmt.Sprint(num(issue["revision"])), Record{"draftId": draft["id"], "version": draft["version"], "hash": draft["hash"], "confirmed": true})
	if code != 201 {
		t.Fatal(o)
	}
	code, o = req(t, s, "POST", "/exports", "p", "export-history", "", Record{"kind": "investigation", "issueId": iid})
	if code != 201 {
		t.Fatal(o)
	}
	exp := data(t, o)
	r := httptest.NewRequest("GET", Base+"/exports/"+str(exp["id"])+"/content", nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "p")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("OLD_EXPIRED_OBSERVATION")) {
		t.Fatal("historical tombstone blocked or leaked through export")
	}
}
func TestAttemptKeyTimeBoundaryErrors(t *testing.T) {
	for _, c := range []struct {
		at   time.Time
		code string
	}{{time.Now().Add(-8 * 24 * time.Hour), "IDEMPOTENCY_EXPIRED"}, {time.Now().Add(10 * time.Minute), "INVALID_INPUT"}} {
		raw, _ := hex.DecodeString(strings.ReplaceAll(keyFor("dated-attempt"), "-", ""))
		millis := c.at.UnixMilli()
		for i := 5; i >= 0; i-- {
			raw[i] = byte(millis >> uint((5-i)*8))
		}
		h := hex.EncodeToString(raw)
		key := fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
		if e := keyProblem(key); e == nil || e.code != c.code {
			t.Fatalf("key boundary error %v", e)
		}
	}
}
func TestRawExportPreservesOriginalAndRechecksRawGrant(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	asset := upload(t, s, "p", "raw-export.txt", "Original bytes\n")
	code, o := req(t, s, "POST", "/exports", "p", "raw-export", "", Record{"kind": "raw", "assetId": asset})
	if code != 201 {
		t.Fatal(o)
	}
	eid := str(data(t, o)["id"])
	download := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", Base+"/exports/"+eid+"/content", nil)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Project-ID", "p")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := download()
	if w.Code != 200 || w.Body.String() != "Original bytes\n" {
		t.Fatal("raw export altered original")
	}
	s.cfg.Tokens[0].Permissions = []string{"export"}
	if w = download(); w.Code != 403 {
		t.Fatal("raw export bypassed grant removal")
	}
}
