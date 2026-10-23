package sdk_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"logan/servergo/internal/workbench"
	"logan/servergo/internal/workbench/connectors"
)

// Actual CLogan bytes, Git and both HTTP adapters are exercised here. The local
// model is a deterministic protocol fixture, not an LLM or a diagnosis-quality
// test; SDK-reported build identity does not attest a physical device.
func TestNativeSDKSupplementContinuesSourceInvestigationHTTP(t *testing.T) {
	for _, tool := range []string{"python3", "cc", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s required", tool)
		}
	}
	module, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "fixture")
	cmd := exec.Command("python3", filepath.Join(module, "script/generate-sdk-workbench-fixture.py"), "--output", fixture)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual CLogan producer: %v %s", err, out)
	}
	native, err := os.ReadFile(filepath.Join(fixture, "native.bin"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("Git fixture %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Synthetic acceptance")
	git("config", "user.email", "acceptance@example.invalid")
	const sourceText = "fun onTimeout() {\n    loading = true // synthetic candidate; causation unknown\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "Login.kt"), []byte(sourceText), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "Login.kt")
	git("commit", "-qm", "synthetic candidate source")
	commit := git("rev-parse", "HEAD")
	source, err := connectors.NewSource(connectors.SourceConfig{Repositories: map[string]string{"fixture": repo}})
	if err != nil {
		t.Fatal(err)
	}
	inputs := make(chan connectors.Investigation, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) != 2 {
			http.Error(w, "invalid model protocol", 400)
			return
		}
		var input connectors.Investigation
		if json.Unmarshal([]byte(payload.Messages[1].Content), &input) != nil || len(input.Evidence) == 0 {
			http.Error(w, "missing snapshot", 400)
			return
		}
		inputs <- input
		citations := []string{}
		for _, ev := range input.Evidence {
			citations = append(citations, ev.ID)
		}
		answer, _ := json.Marshal(connectors.Answer{Summary: "Synthetic protocol fixture: selected evidence received; incident causation remains unknown", Claims: []connectors.Claim{{Type: "hypothesis", Text: "Timeout and manually selected source are investigation candidates only", Citations: citations}}, Unknowns: []string{"Event location, deployed code and root cause remain unverified"}, NextSteps: []string{"Collect event-bound source locations and reproduction evidence"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(answer)}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	model, err := connectors.NewModel(connectors.ModelConfig{Endpoint: provider.URL, Model: "synthetic-protocol-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	protocols, err := connectors.NewProtocols(connectors.ProtocolConfig{NativeKey: []byte("0123456789012345"), NativeIV: []byte("0123456789012345")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := workbench.Config{Dir: t.TempDir(), Source: source, Model: model, Protocols: protocols,
		Projects:   []workbench.Project{{ID: "sdk-investigation", Name: "Synthetic SDK investigation", ModelEnabled: true}},
		Tokens:     []workbench.Token{{Token: "sdk-investigation-admin-0000", Actor: "acceptance", Projects: map[string]string{"sdk-investigation": "admin"}, Permissions: []string{"raw", "export", "source", "model"}}},
		SDKClients: []workbench.SDKClient{{ID: "sdk-fixture", Token: "sdk-investigation-upload-0000", ProjectID: "sdk-investigation", AppID: "com.hxl.xlb.test", Environment: "isolated", Platforms: []string{"android"}}},
	}
	service, err := workbench.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := httptest.NewServer(service)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	type record = map[string]any
	uuid := func() string {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		ms := time.Now().UnixMilli()
		for i := 5; i >= 0; i-- {
			b[i] = byte(ms)
			ms >>= 8
		}
		b[6] = b[6]&15 | 0x70
		b[8] = b[8]&63 | 0x80
		h := hex.EncodeToString(b)
		return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
	}
	request := func(method, path string, body any, want int, headers map[string]string) record {
		t.Helper()
		var wire []byte
		if b, ok := body.([]byte); ok {
			wire = b
		} else if body != nil {
			wire, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, server.URL+workbench.Base+path, bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		if path != "/sdk/native" {
			req.Header.Set("Authorization", "Bearer "+cfg.Tokens[0].Token)
			req.Header.Set("X-Project-ID", "sdk-investigation")
			req.Header.Set("Content-Type", "application/json")
			if method != "GET" {
				req.Header.Set("Idempotency-Key", uuid())
			}
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, resp.StatusCode, raw)
		}
		var envelope struct {
			Data record `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("%s: %v %s", path, err, raw)
		}
		return envelope.Data
	}
	get := func(path string) record { return request("GET", path, nil, 200, nil) }
	post := func(path string, body record) record { return request("POST", path, body, 201, nil) }
	await := func(path string) record {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			row := get(path)
			if row["status"] != "queued" && row["status"] != "running" {
				return row
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %v", path, row)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	id := func(row record) string { return row["id"].(string) }
	rows := func(ws record, bucket string) []any { r, _ := ws[bucket].([]any); return r }
	find := func(ws record, bucket, wanted string) record {
		t.Helper()
		for _, v := range rows(ws, bucket) {
			r := v.(map[string]any)
			if r["id"] == wanted {
				return r
			}
		}
		t.Fatalf("missing %s/%s", bucket, wanted)
		return nil
	}
	upload := func(uploadID string) record {
		return request("POST", "/sdk/native", native, 200, map[string]string{"X-Logan-Upload-Token": cfg.SDKClients[0].Token, "X-Logan-Upload-ID": uploadID, "appId": "com.hxl.xlb.test", "deviceId": "synthetic-device", "platform": "1", "fileDate": "2026-10-19", "appVersion": "1.0-triad", "buildVersion": "triad-42"})
	}
	parse := func(receipt record) record {
		t.Helper()
		runID := receipt["runId"].(string)
		run := await("/parse-runs/" + runID)
		if run["status"] != "succeeded" || run["eventCount"] != float64(4) {
			t.Fatalf("native parse: %v", run)
		}
		for _, v := range get("/parse-runs/" + runID + "/events")["events"].([]any) {
			ev := v.(map[string]any)
			if strings.HasPrefix(fmt.Sprint(ev["content"]), "TRIAD|002|") {
				return ev
			}
		}
		t.Fatal("actual CLogan timeout event missing")
		return nil
	}
	firstUpload := upload("initial-native")
	firstEvent := parse(firstUpload)
	ws := get("/workspace")
	asset := find(ws, "assets", firstUpload["assetId"].(string))
	if asset["origin"] != "sdk" || asset["identitySource"] != "sdk_reported" || asset["issueId"] != "" || len(rows(ws, "issues")) != 0 || len(rows(ws, "evidence")) != 0 {
		t.Fatalf("intake unexpectedly associated an issue: %v", ws)
	}
	if firstEvent["file"] != nil && firstEvent["file"] != "" {
		t.Fatalf("fixture unexpectedly has source location: %v", firstEvent)
	}
	issue := post("/issues", record{"title": "Synthetic native timeout investigation", "context": record{"app": asset["app"], "platform": asset["platform"], "environment": asset["environment"], "device": asset["device"], "version": asset["version"], "build": asset["build"]}})
	iid := id(issue)
	issuePath := "/issues/" + iid
	link := func(assetID string) {
		t.Helper()
		current := get(issuePath)
		linked := request("POST", issuePath+"/log-links", record{"assetId": assetID}, 200, map[string]string{"If-Match": fmt.Sprintf("\"%.0f\"", current["revision"])})
		if !strings.Contains(fmt.Sprint(linked["assetIds"]), assetID) {
			t.Fatalf("explicit link absent: %v", linked)
		}
	}
	addLog := func(receipt, event record) record {
		return post(issuePath+"/evidence", record{"kind": "log", "label": "Native SDK timeout", "originRef": record{"runId": receipt["runId"], "eventIds": []string{id(event)}}})
	}
	link(id(asset))
	log1 := addLog(firstUpload, firstEvent)
	mappingBody := record{"app": asset["app"], "platform": asset["platform"], "build": asset["build"], "repoId": "fixture", "commit": commit}
	mapping := post("/source-mappings", mappingBody)
	request("POST", "/source-mappings", mappingBody, 409, nil)
	fragment := post("/source-resolutions", record{"eventId": id(firstEvent), "mappingId": id(mapping), "path": "Login.kt", "start": 1, "end": 3})
	assertCandidate := func(fragment record) {
		t.Helper()
		if fragment["status"] != "candidate" || fragment["verified"] != false || fragment["identitySource"] != "sdk_reported" || fragment["commit"] != commit || fragment["mappingId"] != id(mapping) {
			t.Fatalf("candidate source promoted or provenance lost: %v", fragment)
		}
	}
	assertCandidate(fragment)
	if fragment["content"] != sourceText {
		t.Fatalf("immutable Git content missing: %v", fragment)
	}
	sourceEvidence := post(issuePath+"/evidence", record{"kind": "source", "originRef": record{"fragmentId": id(fragment)}})
	if sourceEvidence["verified"] != false {
		t.Fatalf("source evidence promoted: %v", sourceEvidence)
	}
	ask := func(question, parent string) record {
		current := get(issuePath)
		body := record{"question": question, "contextRevision": current["contextRevision"], "includeSource": true}
		if parent != "" {
			body["parentTurnId"] = parent
		}
		turn := post(issuePath+"/turns", body)
		turn = await("/turns/" + id(turn))
		if turn["status"] != "completed" {
			t.Fatalf("model protocol turn failed: %v", turn)
		}
		return turn
	}
	checkInput := func(input connectors.Investigation, expected ...string) {
		t.Helper()
		got := []string{}
		for _, ev := range input.Evidence {
			got = append(got, ev.ID)
			if ev.Kind == "source" && (ev.Verified || ev.Status != "candidate" || ev.Provenance == nil || ev.Provenance.IdentitySource != "sdk_reported" || ev.Provenance.Commit != commit) {
				t.Fatalf("provider received upgraded source: %+v", ev)
			}
		}
		sort.Strings(got)
		sort.Strings(expected)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("provider evidence %v, want %v", got, expected)
		}
	}
	checkAnswer := func(turn record, expected ...string) {
		t.Helper()
		wire, _ := json.Marshal(turn["answer"])
		var answer connectors.Answer
		if err := json.Unmarshal(wire, &answer); err != nil {
			t.Fatal(err)
		}
		if len(answer.Claims) != 1 || answer.Claims[0].Type != "hypothesis" || len(answer.Unknowns) == 0 || !strings.Contains(answer.Summary, "causation remains unknown") {
			t.Fatalf("protocol fixture answer lost uncertainty: %+v", answer)
		}
		citations := append([]string{}, answer.Claims[0].Citations...)
		sort.Strings(citations)
		sort.Strings(expected)
		if !reflect.DeepEqual(citations, expected) {
			t.Fatalf("answer citations %v, want exact snapshot IDs %v", citations, expected)
		}
	}
	first := ask("What do the native timeout and candidate source establish?", "")
	input1 := <-inputs
	checkInput(input1, id(log1), id(sourceEvidence))
	checkAnswer(first, id(log1), id(sourceEvidence))
	if len(input1.PriorRounds) != 0 {
		t.Fatal("first round unexpectedly has history")
	}
	firstSnapshot, _ := json.Marshal(first["snapshot"])
	// Identical bytes with a new explicit upload ID form a separate submission.
	supplement := upload("supplement-native")
	secondEvent := parse(supplement)
	if supplement["assetId"] == firstUpload["assetId"] || supplement["runId"] == firstUpload["runId"] || id(secondEvent) == id(firstEvent) {
		t.Fatal("supplement reused initial identities")
	}
	ws = get("/workspace")
	supplementaryAsset := find(ws, "assets", supplement["assetId"].(string))
	if supplementaryAsset["issueId"] != "" || supplementaryAsset["identitySource"] != "sdk_reported" || len(rows(ws, "issues")) != 1 || len(rows(ws, "evidence")) != 2 || len(rows(ws, "turns")) != 1 {
		t.Fatalf("supplement caused automatic issue/evidence/model work: %v", ws)
	}
	if len(get(issuePath)["evidenceIds"].([]any)) != 2 {
		t.Fatal("supplement automatically entered investigation context")
	}
	link(id(supplementaryAsset))
	log2 := addLog(supplement, secondEvent)
	second := ask("Continue with the explicitly linked supplementary native log", id(first))
	input2 := <-inputs
	checkInput(input2, id(log1), id(sourceEvidence), id(log2))
	checkAnswer(second, id(log1), id(sourceEvidence), id(log2))
	if len(input2.PriorRounds) != 1 || input2.PriorRounds[0].Question != input1.Question || input2.PriorRounds[0].Answer.Summary != first["answer"].(map[string]any)["summary"] {
		t.Fatalf("follow-up lost history: %+v", input2.PriorRounds)
	}
	if second["parentTurnId"] != id(first) {
		t.Fatal("follow-up lost parent identity")
	}
	freshSnapshot, _ := json.Marshal(get("/turns/" + id(first))["snapshot"])
	if !bytes.Equal(firstSnapshot, freshSnapshot) || len(second["snapshot"].(map[string]any)["evidenceIds"].([]any)) != 3 {
		t.Fatal("supplement changed old snapshot or failed to enter new snapshot")
	}
	assertCandidate(get("/source-fragments/" + id(fragment)))
	hypothesis := post(issuePath+"/hypotheses", record{"text": "Timeout may relate to terminal state; causation remains unknown", "evidenceIds": []string{id(log1), id(sourceEvidence), id(log2)}})
	verification := post(issuePath+"/verifications", record{"purpose": "hypothesis", "result": "uncertain", "hypothesisId": id(hypothesis), "hypothesisVersion": hypothesis["version"], "method": "Review native logs and candidate source", "expected": "Event-bound source and reproduction", "observed": "SDK build assertion and manually selected source only", "evidenceIds": []string{id(log1), id(sourceEvidence), id(log2)}})
	if verification["result"] != "uncertain" {
		t.Fatal("uncertain verification changed", verification)
	}
	issue = get(issuePath)
	preview := post(issuePath+"/handoff-previews", record{"mode": "ai", "turnId": id(second), "summary": "Synthetic acceptance: timeout observed; source and root cause remain unverified", "status": "investigating", "owner": "acceptance", "issueRevision": issue["revision"], "contextRevision": issue["contextRevision"]})
	handoff := request("POST", issuePath+"/handoffs", record{"draftId": id(preview), "version": preview["version"], "hash": preview["hash"], "confirmed": true}, 201, map[string]string{"If-Match": fmt.Sprintf("\"%.0f\"", issue["revision"])})
	if handoff["turnId"] != id(second) || get(issuePath)["status"] != "investigating" {
		t.Fatalf("handoff lost second round or resolved issue: %v", handoff)
	}
}
