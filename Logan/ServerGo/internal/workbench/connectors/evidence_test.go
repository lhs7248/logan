package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelPreservesEvidenceUncertaintyAndProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Messages) != 2 || !strings.Contains(request.Messages[0].Content, "unverified, candidate, or manual") || !strings.Contains(request.Messages[0].Content, "historical answers are not new facts") || !strings.Contains(request.Messages[0].Content, "operator_declared") || !strings.Contains(request.Messages[0].Content, "IdentitySource=sdk_reported") {
			t.Error("missing uncertainty policy")
		}
		var snapshot Investigation
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &snapshot); err != nil {
			t.Error(err)
		}
		if len(snapshot.Evidence) != 4 {
			t.Error("missing evidence")
			return
		}
		candidate, matched, manual := snapshot.Evidence[0], snapshot.Evidence[1], snapshot.Evidence[2]
		if candidate.Verified || candidate.Status != "candidate" || candidate.Availability != "available" || candidate.OriginRef["reason"] != "line selected by user" || candidate.Provenance.Commit != strings.Repeat("a", 40) {
			t.Errorf("candidate lost %+v", candidate)
		}
		if !matched.Verified || matched.Status != "matched" || matched.Provenance.IdentitySource != "operator_declared" || matched.Provenance.Build != "42" || matched.Provenance.MappingID != "map-1" {
			t.Errorf("proof lost %+v", matched)
		}
		if manual.Verified || manual.Status != "manual" {
			t.Errorf("manual incorrectly verified %+v", manual)
		}
		if incomplete := snapshot.Evidence[3]; incomplete.Verified || incomplete.Status != "candidate" {
			t.Errorf("incomplete proof retained confidence %+v", incomplete)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"summary":"Need verification","claims":[],"unknowns":["incident code identity"],"nextSteps":["verify mapping"]}`}}}})
	}))
	defer srv.Close()
	m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test"})
	proof := &SourceProvenance{IdentitySource: "operator_declared", MappingID: "map-1", RepoID: "app", App: "mobile", Platform: "android", Build: "42", Commit: strings.Repeat("a", 40), Path: "Main.kt", Start: 2, End: 3, ContentHash: digest([]byte("matched code")), BlobHash: strings.Repeat("b", 40)}
	_, err := m.Investigate(context.Background(), Investigation{Question: "Why?", Evidence: []Evidence{{ID: "candidate", Kind: "source", Content: "candidate code", Status: "candidate", Availability: "available", OriginRef: map[string]any{"reason": "line selected by user"}, Provenance: proof}, {ID: "matched", Kind: "source", Content: "matched code", Verified: true, Status: "matched", Availability: "available", Provenance: proof}, {ID: "manual", Kind: "manual", Content: "pasted code", Verified: true, Status: "manual", Availability: "available"}, {ID: "incomplete", Kind: "source", Content: "code", Verified: true, Status: "matched", Availability: "available"}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestModelMetadataBudgetsBeforeOutboundCall(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test", MaxRequestBytes: 32 << 10})
	deep := map[string]any{"end": "x"}
	for i := 0; i < 10; i++ {
		deep = map[string]any{"nested": deep}
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	for _, metadata := range []map[string]any{{"large": strings.Repeat("x", 17<<10)}, deep, cyclic} {
		if _, err := m.Investigate(context.Background(), Investigation{Question: "why", Evidence: []Evidence{{ID: "ev", OriginRef: metadata}}}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("metadata: %v", err)
		}
	}
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why", Evidence: []Evidence{{ID: "ev", OriginRef: map[string]any{"unsupported": func() {}}}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsupported metadata: %v", err)
	}
	evidence := []Evidence{}
	for i := 0; i < 4; i++ {
		evidence = append(evidence, Evidence{ID: string(rune('a' + i)), OriginRef: map[string]any{"value": strings.Repeat("x", 10<<10)}})
	}
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why", Evidence: evidence}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("cumulative metadata: %v", err)
	}
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why", Evidence: []Evidence{{ID: "proof", Provenance: &SourceProvenance{MappingID: strings.Repeat("m", 33<<10)}}}}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("provenance budget: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("oversized or invalid metadata sent to provider")
	}
}
