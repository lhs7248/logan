package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"logan/servergo/internal/workbench/connectors"
)

// Exercise the actual repository adapter through authenticated API operations.
// The repository and log are synthetic, and an operator declaration does not
// establish that this commit was running on a user's device.
func TestSourceAPIUsesImmutableRepositoryAndIndependentAssetIdentity(t *testing.T) {
	repo, canonicalError := filepath.EvalSymlinks(t.TempDir())
	if canonicalError != nil {
		t.Fatal(canonicalError)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s", args, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	git("config", "user.name", "Acceptance")
	git("config", "user.email", "acceptance@example.invalid")
	path := filepath.Join(repo, "Login.js")
	if e := os.WriteFile(path, []byte("export function finish(state) {\n  state.loading = true;\n}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", "Login.js")
	git("commit", "-qm", "initial synthetic state")
	oldCommit := git("rev-parse", "HEAD")
	if e := os.WriteFile(path, []byte("export function finish(state) {\n  state.loading = false;\n}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("commit", "-qam", "synthetic comparison only")
	newCommit := git("rev-parse", "HEAD")
	source, e := connectors.NewSource(connectors.SourceConfig{Repositories: map[string]string{"fixture": repo}})
	if e != nil {
		t.Fatal(e)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) != 2 {
			w.WriteHeader(400)
			return
		}
		var input connectors.Investigation
		if json.Unmarshal([]byte(payload.Messages[1].Content), &input) != nil || len(input.Evidence) == 0 {
			w.WriteHeader(400)
			return
		}
		answer, _ := json.Marshal(Record{"summary": "Synthetic protocol acceptance: evidence present; root cause remains unknown", "claims": []Record{{"type": "fact", "text": "Selected evidence is available for review", "citations": []string{input.Evidence[0].ID}}}, "unknowns": []string{"Real incident identity and causation have not been validated"}, "nextSteps": []string{"Review actual reproduction evidence"}})
		_ = json.NewEncoder(w).Encode(Record{"choices": []Record{{"message": Record{"content": string(answer)}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	model, e := connectors.NewModel(connectors.ModelConfig{Endpoint: provider.URL, Model: "synthetic-protocol-acceptance"})
	if e != nil {
		t.Fatal(e)
	}
	s, e := Open(Config{Dir: t.TempDir(), Source: source, Model: model, Projects: []Project{{ID: "p", Name: "Synthetic", ModelEnabled: true}}, Tokens: []Token{{Token: "secret", Actor: "alice", Projects: map[string]string{"p": "admin"}, Permissions: []string{"source", "raw", "export", "model"}}}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	addMapping := func(build, commit string) string {
		t.Helper()
		code, o := req(t, s, "POST", "/source-mappings", "p", "mapping-"+build, "", Record{"app": "fixture", "platform": "web", "build": build, "repoId": "fixture", "commit": commit})
		if code != 201 {
			t.Fatal(o)
		}
		return str(data(t, o)["id"])
	}
	oldMapping := addMapping("old", oldCommit)
	newMapping := addMapping("new", newCommit)
	importEvent := func(build string) string {
		t.Helper()
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		meta, _ := json.Marshal(Record{"app": "fixture", "platform": "web", "build": build})
		_ = mw.WriteField("metadata", string(meta))
		p, _ := mw.CreateFormFile("files", "fixture-"+build+".jsonl")
		_, _ = io.WriteString(p, "{\"message\":\"observed loading state\",\"file\":\"Login.js\",\"sourceLine\":2}\n")
		_ = mw.Close()
		r := httptest.NewRequest("POST", Base+"/imports", &b)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Project-ID", "p")
		r.Header.Set("Idempotency-Key", keyFor("source-import-"+build))
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var o map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &o)
		if w.Code != 201 {
			t.Fatal(o)
		}
		asset := record(sliceOf(data(t, o)["assets"])[0])
		_, o = req(t, s, "POST", "/assets/"+str(asset["id"])+"/parse-runs", "p", "source-parse-"+build, "", Record{"format": "jsonl", "mode": "strict"})
		run := awaitRun(t, s, str(data(t, o)["id"]))
		_, o = req(t, s, "GET", "/parse-runs/"+str(run["id"])+"/events", "p", "", "", nil)
		return str(record(sliceOf(data(t, o)["events"])[0])["id"])
	}
	oldEvent := importEvent("old")
	newEvent := importEvent("new")
	code, o := req(t, s, "POST", "/source-resolutions", "p", "wrong-version", "", Record{"eventId": oldEvent, "mappingId": newMapping, "path": "Login.js", "start": 1, "end": 3})
	if code != 200 || data(t, o)["status"] != "candidate" || data(t, o)["content"] != nil {
		t.Fatalf("wrong build supplied source as fact: %v", o)
	}
	resolve := func(id, key string) Record {
		t.Helper()
		code, o := req(t, s, "POST", "/source-resolutions", "p", key, "", Record{"eventId": id, "start": 1, "end": 3})
		if code != 201 {
			t.Fatal(o)
		}
		return Record(data(t, o))
	}
	// The requested range can exceed the actual file length. Only the returned
	// fragment's range establishes whether the event location was covered.
	s.mu.Lock()
	s.state.bucket("events")[oldEvent]["sourceLine"] = 50
	s.mu.Unlock()
	code, o = req(t, s, "POST", "/source-resolutions", "p", "truncated-range", "", Record{"eventId": oldEvent, "start": 1, "end": 100})
	if code != 201 || data(t, o)["verified"] != false || data(t, o)["status"] != "candidate" {
		t.Fatalf("truncated source fragment incorrectly covers event line 50: %v", o)
	}
	s.mu.Lock()
	s.state.bucket("events")[oldEvent]["sourceLine"] = 2
	s.mu.Unlock()
	left := resolve(oldEvent, "old-fragment")
	right := resolve(newEvent, "new-fragment")
	if left["commit"] != oldCommit || left["mappingId"] != oldMapping || !strings.Contains(str(left["content"]), "loading = true") || right["commit"] != newCommit {
		t.Fatalf("source not pinned to event build: %v / %v", left, right)
	}
	code, o = req(t, s, "GET", "/source-diffs?leftRef="+str(left["id"])+"&rightRef="+str(right["id"]), "p", "", "", nil)
	if code != 200 || !strings.Contains(str(data(t, o)["content"]), "loading = false") {
		t.Fatalf("actual Git comparison missing: %v", o)
	}
	_, o = req(t, s, "POST", "/issues", "p", "source-issue", "", Record{"title": "Source investigation"})
	iid := str(data(t, o)["id"])
	code, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "source-evidence", "", Record{"kind": "source", "originRef": Record{"fragmentId": left["id"]}})
	if code != 201 || str(data(t, o)["contentHash"]) == "" {
		t.Fatal(o)
	}
	// This loopback provider only validates transport/citations; it makes no real diagnosis.
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	issue := data(t, o)
	code, o = req(t, s, "POST", "/issues/"+iid+"/turns", "p", "source-round-1", "", Record{"question": "What does the selected source establish?", "contextRevision": issue["contextRevision"]})
	if code != 201 {
		t.Fatal(o)
	}
	first := awaitTurn(t, s, str(data(t, o)["id"]))
	if first["status"] != "completed" {
		t.Fatal(first)
	}
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "source-supplement", "", Record{"kind": "manual", "content": "Synthetic follow-up: reproduction is still unavailable"})
	if o["error"] != nil {
		t.Fatal(o)
	}
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	issue = data(t, o)
	code, o = req(t, s, "POST", "/issues/"+iid+"/turns", "p", "source-round-2", "", Record{"question": "Continue using the added observation", "parentTurnId": first["id"], "contextRevision": issue["contextRevision"]})
	if code != 201 {
		t.Fatal(o)
	}
	second := awaitTurn(t, s, str(data(t, o)["id"]))
	if second["status"] != "completed" || len(stringsOf(record(first["snapshot"])["evidenceIds"])) != 1 || len(stringsOf(record(second["snapshot"])["evidenceIds"])) != 2 {
		t.Fatal("supplement changed old snapshot or failed to reach next round", second)
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoff-previews", "p", "source-preview", "", Record{"mode": "ai", "turnId": second["id"], "summary": "Synthetic acceptance only: real root cause remains unverified", "status": "investigating", "owner": "alice", "issueRevision": issue["revision"], "contextRevision": issue["contextRevision"]})
	if code != 201 {
		t.Fatal(o)
	}
	preview := data(t, o)
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoffs", "p", "source-handoff", fmt.Sprint(num(issue["revision"])), Record{"draftId": preview["id"], "version": preview["version"], "hash": preview["hash"], "confirmed": true})
	if code != 201 || data(t, o)["turnId"] != second["id"] {
		t.Fatal("configured AI handoff did not retain its source round", o)
	}
	code, _ = req(t, s, "POST", "/source-mappings", "p", "overwrite-mapping", "", Record{"app": "fixture", "platform": "web", "build": "old", "repoId": "fixture", "commit": newCommit})
	if code != 409 {
		t.Fatal("immutable build mapping was overwritten")
	}
}
