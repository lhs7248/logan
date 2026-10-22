package workbench

import (
	"encoding/json"
	"strings"
	"time"

	"logan/servergo/internal/workbench/connectors"
)

func (q *request) createHypothesis(issue, b Record) (any, int, *apiError) {
	text := strings.TrimSpace(str(b["text"]))
	if text == "" || len(text) > 16000 {
		return nil, 0, fail(422, "HYPOTHESIS_INVALID", "Hypothesis text must be 1–16000 bytes")
	}
	h := q.s.state.add("hypotheses", q.project, Record{"issueId": issue["id"], "text": text, "version": 1, "revision": 1, "status": "pending", "actor": q.actor.Actor, "expiresAt": time.Now().UTC().Add(q.s.cfg.Retention).Format(time.RFC3339Nano)})
	return h, 201, nil
}
func (s *Server) answerWithHypotheses(turn Record, answer connectors.Answer) Record {
	raw, _ := json.Marshal(answer)
	r := Record{}
	_ = json.Unmarshal(raw, &r)
	for _, value := range sliceOf(r["claims"]) {
		claim := record(value)
		if claim["type"] != "hypothesis" {
			continue
		}
		h := s.state.add("hypotheses", str(turn["projectId"]), Record{"issueId": turn["issueId"], "turnId": turn["id"], "text": claim["text"], "evidenceIds": claim["citations"], "version": 1, "revision": 1, "status": "pending", "expiresAt": turn["expiresAt"], "actor": turn["actor"]})
		claim["hypothesisId"] = h["id"]
		claim["hypothesisVersion"] = h["version"]
	}
	return r
}
