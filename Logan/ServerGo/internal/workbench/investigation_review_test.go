package workbench

import (
	"fmt"
	"testing"
	"time"
)

func TestExpiredEditableDraftCanRestartWithFreshText(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "audit-issue", "", Record{"title": "Long-lived"})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "audit-old-evidence", "", Record{"kind": "manual", "content": "OLD_EXPIRED_EVIDENCE"})
	oldEvidence := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/handoff-drafts", "p", "audit-draft", "", Record{"mode": "manual", "summary": "old"})
	did := str(data(t, o)["id"])
	s.mu.Lock()
	past := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.state.bucket("editableDrafts")[did]["expiresAt"] = past
	s.state.bucket("evidence")[oldEvidence]["expiresAt"] = past
	s.mu.Unlock()
	code, o := req(t, s, "PATCH", "/handoff-drafts/"+did, "p", "audit-refresh", "1", Record{"mode": "manual", "summary": "Fresh independent observation"})
	if code != 410 {
		t.Fatalf("expired draft must remain immutable: status=%d data=%v", code, o)
	}
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "audit-fresh-evidence", "", Record{"kind": "manual", "content": "Fresh independent evidence"})
	freshEvidence := str(data(t, o)["id"])
	_, o = req(t, s, "GET", "/issues/"+iid, "p", "", "", nil)
	code, o = req(t, s, "PATCH", "/issues/"+iid+"/context", "p", "audit-remove-expired", fmt.Sprint(num(data(t, o)["contextRevision"])), Record{"remove": []string{oldEvidence}})
	if code != 200 {
		t.Fatal(o)
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoff-drafts", "p", "audit-new-draft", "", Record{"mode": "manual", "summary": "Fresh independent observation"})
	if code != 201 || data(t, o)["summary"] != "Fresh independent observation" || str(data(t, o)["id"]) == did {
		t.Fatalf("fresh draft cannot persist: status=%d data=%v", code, o)
	}
	if ids := stringsOf(data(t, o)["evidenceIds"]); len(ids) != 1 || ids[0] != freshEvidence {
		t.Fatal("new draft retained expired dependencies", o)
	}
	_, o = req(t, s, "GET", "/handoff-drafts/"+did, "p", "", "", nil)
	if data(t, o)["summary"] != nil {
		t.Fatal("expired draft text resurrected")
	}

}
func TestIncludeSourceFalseExcludesCode(t *testing.T) {
	s, received, release := modelServer(t)
	_, o := req(t, s, "POST", "/issues", "p", "audit-aiissue", "", Record{"title": "Source opt out"})
	iid := str(data(t, o)["id"])
	s.mu.Lock()
	ev := s.state.add("evidence", "p", Record{"issueId": iid, "kind": "source", "content": "SENSITIVE_SOURCE_BODY", "verified": false, "availability": "available", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	s.state.bucket("issues")[iid]["evidenceIds"] = []string{str(ev["id"])}
	s.mu.Unlock()
	code, o := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "audit-not-send-source", "", Record{"question": "What is missing?", "contextRevision": 1, "includeSource": false})
	if code != 201 {
		t.Fatal(o)
	}
	select {
	case in := <-received:
		release <- struct{}{}
		for _, e := range in.Evidence {
			if e.Kind == "source" {
				t.Fatalf("includeSource=false sent %s", e.Content)
			}
		}
		turn := awaitTurn(t, s, str(data(t, o)["id"]))
		manifest := record(sliceOf(turn["retrievalManifest"])[0])
		if manifest["included"] != false || manifest["reason"] != "source_excluded" || num(manifest["sentBytes"]) != 0 || str(manifest["sentContentHash"]) != "" {
			t.Fatal("excluded-source manifest claims sent data", manifest)
		}
		code, next := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "audit-source-free-followup", "", Record{"question": "Continue without source", "contextRevision": 1, "includeSource": false, "parentTurnId": turn["id"]})
		if code != 201 {
			t.Fatal("source-free followup blocked by excluded snapshot", next)
		}
		select {
		case followup := <-received:
			if len(followup.Evidence) != 0 || len(followup.PriorRounds) != 1 {
				t.Fatal("source-free followup input invalid", followup)
			}
			followupID := str(data(t, next)["id"])
			code, canceled := req(t, s, "POST", "/turns/"+followupID+"/cancel", "p", "audit-cancel-source-free", "", Record{})
			if code != 200 {
				t.Fatal(canceled)
			}
			release <- struct{}{}
			code, retry := req(t, s, "POST", "/turns/"+followupID+"/retries", "p", "audit-retry-source-free", "", Record{})
			if code != 201 || data(t, retry)["includeSource"] != false || data(t, retry)["retryOf"] != followupID {
				t.Fatal("retry lost source opt-out or origin", retry)
			}
			select {
			case retryInput := <-received:
				release <- struct{}{}
				if len(retryInput.Evidence) != 0 {
					t.Fatal("retry sent excluded source", retryInput)
				}
			case <-time.After(time.Second):
				t.Fatal("retry provider input missing")
			}
		case <-time.After(time.Second):
			t.Fatal("followup provider input missing")
		}
	case <-time.After(time.Second):
		t.Fatal("no provider input")
	}
}

func TestTurnRejectsMismatchedExpectedEvidenceMembers(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "expected-issue", "", Record{"title": "Evidence membership"})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "expected-proof", "", Record{"kind": "manual", "content": "Actual selected observation"})
	eid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "expected-mismatch", "", Record{"question": "Why?", "contextRevision": 2, "expectedEvidenceVersionIds": []string{}})
	if code != 409 {
		t.Fatalf("mismatched expected membership accepted: %d %v", code, o)
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/turns", "p", "expected-matched", "", Record{"question": "Why?", "contextRevision": 2, "expectedEvidenceVersionIds": []string{eid}})
	if code != 201 {
		t.Fatalf("correct membership rejected: %d %v", code, o)
	}
}

func TestRestrictedEditableDraftAllowsIndependentManualReplacement(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, o := req(t, s, "POST", "/issues", "p", "restricted-draft-issue", "", Record{"title": "Continue manually"})
	iid := str(data(t, o)["id"])
	s.mu.Lock()
	ev := s.state.add("evidence", "p", Record{"issueId": iid, "kind": "source", "content": "PRIVATE_SOURCE", "availability": "available"})
	s.state.bucket("issues")[iid]["evidenceIds"] = []string{str(ev["id"])}
	s.mu.Unlock()
	_, o = req(t, s, "POST", "/issues/"+iid+"/handoff-drafts", "p", "restricted-draft-old", "", Record{"mode": "manual", "summary": "Source-derived text"})
	did := str(data(t, o)["id"])
	s.mu.Lock()
	s.cfg.Tokens[0].Permissions = []string{"model", "raw", "export"}
	s.mu.Unlock()
	code, o := req(t, s, "PATCH", "/handoff-drafts/"+did, "p", "restricted-draft-patch", "1", Record{"summary": "New manual text"})
	if code != 403 {
		t.Fatalf("restricted draft edited as if usable: %d %v", code, o)
	}
	code, o = req(t, s, "PATCH", "/issues/"+iid+"/context", "p", "restricted-remove-source", "1", Record{"remove": []string{str(ev["id"])}})
	if code != 200 {
		t.Fatal(o)
	}
	code, o = req(t, s, "POST", "/issues/"+iid+"/handoff-drafts", "p", "restricted-draft-new", "", Record{"mode": "manual", "summary": "Independent observation only"})
	if code != 201 || data(t, o)["summary"] != "Independent observation only" {
		t.Fatalf("independent manual replacement blocked: %d %v", code, o)
	}
}

func TestExcludedSourceDoesNotRestrictLogOnlyModelTurn(t *testing.T) {
	s, received, release := modelServer(t)
	_, o := req(t, s, "POST", "/issues", "p", "log-only-issue", "", Record{"title": "Log-only investigation"})
	iid := str(data(t, o)["id"])
	_, o = req(t, s, "POST", "/issues/"+iid+"/evidence", "p", "log-only-observation", "", Record{"kind": "manual", "content": "Observed request timeout"})
	manualID := str(data(t, o)["id"])
	s.mu.Lock()
	ev := s.state.add("evidence", "p", Record{"issueId": iid, "kind": "source", "content": "PRIVATE_SOURCE", "contentHash": "source-hash", "availability": "available", "originRef": Record{"repoId": "private-repo"}, "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	s.state.bucket("issues")[iid]["evidenceIds"] = []string{manualID, str(ev["id"])}
	s.cfg.Tokens[0].Permissions = []string{"model", "export"}
	s.mu.Unlock()
	code, o := req(t, s, "POST", "/issues/"+iid+"/turns", "p", "log-only-turn", "", Record{"question": "What does the observation establish?", "contextRevision": 2, "includeSource": false})
	if code != 201 {
		t.Fatal(o)
	}
	tid := str(data(t, o)["id"])
	select {
	case in := <-received:
		release <- struct{}{}
		if len(in.Evidence) != 1 || in.Evidence[0].ID != manualID {
			t.Fatal("wrong sent evidence", in)
		}
	case <-time.After(time.Second):
		t.Fatal("excluded source prevented authorized model request")
	}
	turn := awaitTurn(t, s, tid)
	if turn["status"] != "completed" || turn["answer"] == nil || turn["availability"] == "restricted" {
		t.Fatal("log-only answer restricted by excluded source", turn)
	}
	snapshot := record(turn["snapshot"])
	if len(stringsOf(snapshot["evidenceIds"])) != 1 || len(stringsOf(snapshot["contextEvidenceIds"])) != 2 || len(sliceOf(snapshot["excludedEvidence"])) != 1 {
		t.Fatal("snapshot lost actual scope or selection identity", snapshot)
	}
	if q := (request{s: s, project: "p", actor: &s.cfg.Tokens[0]}); q.hasSourceRefs(turn, map[string]bool{}) {
		t.Fatal("excluded metadata retains active source dependency")
	}
	s.mu.Lock()
	s.state.bucket("evidence")[str(ev["id"])]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	code, o = req(t, s, "POST", "/issues/"+iid+"/turns", "p", "log-only-followup-after-source-expiry", "", Record{"question": "Continue from the observation", "contextRevision": 2, "includeSource": false, "parentTurnId": tid})
	if code != 201 {
		t.Fatal("excluded source expiry blocked followup", o)
	}
	select {
	case input := <-received:
		release <- struct{}{}
		if len(input.Evidence) != 1 || len(input.PriorRounds) != 1 {
			t.Fatal("log-only followup lost allowed context", input)
		}
	case <-time.After(time.Second):
		t.Fatal("source expiry blocked model followup")
	}
	if fresh := awaitTurn(t, s, str(data(t, o)["id"])); fresh["status"] != "completed" || fresh["answer"] == nil {
		t.Fatal("source expiry hid log-only followup", fresh)
	}
}
