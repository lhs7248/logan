package workbench

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"logan/servergo/internal/workbench/connectors"
)

func modelServer(t *testing.T) (*Server, chan connectors.Investigation, chan struct{}) {
	t.Helper()
	received := make(chan connectors.Investigation, 10)
	release := make(chan struct{}, 10)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) != 2 {
			t.Error("invalid provider request")
			w.WriteHeader(400)
			return
		}
		var input connectors.Investigation
		if json.Unmarshal([]byte(payload.Messages[1].Content), &input) != nil {
			t.Error("invalid model input")
			w.WriteHeader(400)
			return
		}
		received <- input
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		claims := []Record{}
		if len(input.Evidence) > 0 {
			claims = append(claims, Record{"type": "hypothesis", "text": "Possible missing state transition", "citations": []string{input.Evidence[0].ID}})
		}
		answer, _ := json.Marshal(Record{"summary": "Review observations", "claims": claims, "unknowns": []string{"More evidence required"}, "nextSteps": []string{"Reproduce manually"}})
		_ = json.NewEncoder(w).Encode(Record{"choices": []Record{{"message": Record{"content": string(answer)}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	model, err := connectors.NewModel(connectors.ModelConfig{Endpoint: provider.URL, Model: "test-provider"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Config{Dir: t.TempDir(), Model: model, Projects: []Project{{ID: "p", Name: "Test", ModelEnabled: true}}, Tokens: []Token{{Token: "secret", Actor: "alice", Projects: map[string]string{"p": "reviewer"}, Permissions: []string{"model", "source", "export", "raw"}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, received, release
}
func awaitTurn(t *testing.T, s *Server, tid string) Record {
	t.Helper()
	for i := 0; i < 300; i++ {
		_, o := req(t, s, "GET", "/turns/"+tid, "p", "", "", nil)
		r := data(t, o)
		if r["status"] != "queued" && r["status"] != "running" {
			return r
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("turn did not complete")
	return nil
}
func prepareTurn(t *testing.T, s *Server, tag string) (string, string, string) {
	t.Helper()
	_, o := req(t, s, "POST", "/issues", "p", "issue-"+tag, "", Record{"title": "Issue " + tag})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "evidence-"+tag, "", Record{"kind": "manual", "content": "password=TOPSECRET user@example.com observed timeout"})
	eid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "turn-"+tag, "", Record{"question": "Why?", "contextRevision": 2})
	if code != 201 {
		t.Fatal(o)
	}
	return iid, eid, str(data(t, o)["id"])
}
func TestConfiguredModelSnapshotMaskingHypothesesAndVerification(t *testing.T) {
	s, received, release := modelServer(t)
	iid, eid, tid := prepareTurn(t, s, "snapshot")
	var in connectors.Investigation
	select {
	case in = <-received:
	case <-time.After(time.Second):
		t.Fatal("provider request missing")
	}
	if len(in.Evidence) != 1 || bytes.Contains([]byte(in.Evidence[0].Content), []byte("TOPSECRET")) || in.Evidence[0].Verified || in.Evidence[0].Status != "manual" {
		t.Fatalf("unsafe input %+v", in)
	}
	code, o := req(t, s, "PATCH", "/issues/"+iid+"/context", "p", "remove", "2", Record{"remove": []string{eid}})
	if code != 200 {
		t.Fatal(o)
	}
	release <- struct{}{}
	turn := awaitTurn(t, s, tid)
	if turn["status"] != "completed" {
		t.Fatal(turn)
	}
	answer := record(turn["answer"])
	claim := record(sliceOf(answer["claims"])[0])
	hid := str(claim["hypothesisId"])
	if hid == "" || num(claim["hypothesisVersion"]) != 1 {
		t.Fatal("missing stable hypothesis identity")
	}
	manifest := record(sliceOf(turn["retrievalManifest"])[0])
	if manifest["sentContentHash"] != digest([]byte(in.Evidence[0].Content)) {
		t.Fatalf("manifest does not represent sent content: %v", manifest)
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/verifications", "p", "hypothesis-verify", "", Record{"purpose": "hypothesis", "hypothesisId": hid, "hypothesisVersion": 1, "method": "reproduce", "expected": "timeout", "observed": "no timeout", "result": "refuted", "evidenceIds": []string{eid}})
	if code != 201 {
		t.Fatal(o)
	}
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	if num(data(t, o)["contextRevision"]) != 4 {
		t.Fatal("verification was not added to next-turn context")
	}
	code, _ = req(t, s, "POST", "/issues/"+iid+"/verifications", "p", "bad-hypothesis", "", Record{"purpose": "hypothesis", "hypothesisId": hid, "hypothesisVersion": 2, "method": "test", "expected": "x", "observed": "y", "result": "refuted"})
	if code != 422 {
		t.Fatal("hypothesis version mismatch accepted")
	}
}
func TestModelCancellationAndEvidenceExpiryPreventPublication(t *testing.T) {
	s, received, release := modelServer(t)
	_, eid, tid := prepareTurn(t, s, "expired")
	<-received
	s.mu.Lock()
	s.state.bucket("evidence")[eid]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	release <- struct{}{}
	turn := awaitTurn(t, s, tid)
	if turn["status"] != "failed" || turn["answer"] != nil {
		t.Fatal("expired snapshot published answer")
	}
	_, _, tid = prepareTurn(t, s, "cancel")
	<-received
	code, o := req(t, s, "POST", "/turns/"+tid+"/cancel", "p", "cancel", "", Record{})
	if code != 200 {
		t.Fatal(o)
	}
	if turn = awaitTurn(t, s, tid); turn["status"] != "canceled" || turn["answer"] != nil {
		t.Fatal("canceled model output published")
	}
}

func TestModelGlobalBoundAndLatePermissionRevocation(t *testing.T) {
	s, received, release := modelServer(t)
	_, _, first := prepareTurn(t, s, "one")
	<-received
	_, _, second := prepareTurn(t, s, "two")
	<-received
	_, o := req(t, s, "POST", "/issues", "p", "issue-three", "", Record{"title": "Third"})
	iid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "turn-three", "", Record{"question": "Why?", "contextRevision": 1})
	if code != 429 {
		t.Fatalf("unbounded provider requests %d %v", code, o)
	}
	s.mu.Lock()
	s.cfg.Tokens[0].Permissions = []string{"raw", "export"}
	s.mu.Unlock()
	release <- struct{}{}
	release <- struct{}{}
	for _, tid := range []string{first, second} {
		r := awaitTurn(t, s, tid)
		if r["status"] != "failed" || r["answer"] != nil {
			t.Fatal("permission revoked during provider request but answer published")
		}
	}
}
