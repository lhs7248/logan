package workbench

import (
	"encoding/json"
	"strings"
	"time"
)

func recordExpired(r Record) bool {
	t, e := time.Parse(time.RFC3339Nano, str(r["expiresAt"]))
	return e == nil && !t.After(time.Now())
}
func scrubExpired(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return scrubExpired(Record(v))
	case Record:
		r := v
		if recordExpired(r) {
			for _, key := range []string{"content", "fields", "answer", "summary", "question", "observed", "expected", "method", "text", "before", "after", "reason", "targetValue", "bindingConfirmed"} {
				if _, ok := r[key]; ok {
					r[key] = nil
				}
			}
			r["availability"] = "expired"
		}
		for k, x := range r {
			r[k] = scrubExpired(x)
		}
		return r
	case []any:
		for i, x := range v {
			v[i] = scrubExpired(x)
		}
		return v
	case []Record:
		for i, r := range v {
			v[i] = scrubExpired(r).(Record)
		}
		return v
	}
	return value
}
func (s *Server) purgeExpired() {
	for tid, turn := range s.state.bucket("turns") {
		if turn["status"] == "queued" || turn["status"] == "running" {
			q := request{s: s, project: str(turn["projectId"])}
			if q.hasExpiredRefs(turn, map[string]bool{}) || q.snapshotUnavailable(record(turn["snapshot"])) {
				if cancel := s.cancels[tid]; cancel != nil {
					cancel()
				}
				turn["status"] = "failed"
				turn["answer"] = nil
				turn["error"] = Record{"code": "SOURCE_EXPIRED", "message": "Evidence expired during investigation"}
				turn["finishedAt"] = now()
			}
		}
	}
	for aid, a := range s.state.bucket("assets") {
		if recordExpired(a) {
			delete(s.state.bucket("raw"), aid)
		}
	}
	for kind, bucket := range s.state.Objects {
		if kind == "raw" {
			continue
		}
		for _, r := range bucket {
			if kind == "exports" && r["payload"] != nil {
				delete(r, "content")
			}
			q := request{s: s, project: str(r["projectId"])}
			if dependentBody(r) && q.hasExpiredRefs(r, map[string]bool{}) {
				r["expiresAt"] = time.Now().Add(-time.Nanosecond).UTC().Format(time.RFC3339Nano)
			}
			s.scrubExpiredCopies(r, str(r["projectId"]))
		}
	}
	for key, r := range s.state.Idempotency {
		var body Record
		if json.Unmarshal(r.Body, &body) == nil {
			parts := strings.Split(key, "\x00")
			if len(parts) > 0 {
				q := request{s: s, project: parts[0]}
				if q.hasExpiredRefs(body, map[string]bool{}) {
					if data := record(body["data"]); len(data) > 0 {
						data["expiresAt"] = time.Now().Add(-time.Nanosecond).UTC().Format(time.RFC3339Nano)
					}
				}
			}
			project := ""
			if len(parts) > 0 {
				project = parts[0]
			}
			s.scrubExpiredCopies(body, project)
			r.Body, _ = json.Marshal(body)
			s.state.Idempotency[key] = r
		}
	}
}
func (s *Server) startReaper() {
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				if !s.closed && s.unhealthy == nil {
					s.persistJob(func() { s.purgeExpired() })
				}
				s.mu.Unlock()
			}
		}
	}()
}

// scrubExpiredCopies follows original IDs even in immutable embedded snapshots;
// otherwise an old copied expiry could retain a body after its source expires.
func (s *Server) scrubExpiredCopies(value any, project string) any {
	switch r := value.(type) {
	case map[string]any:
		return s.scrubExpiredCopies(Record(r), project)
	case Record:
		if p := str(r["projectId"]); p != "" {
			project = p
		}
		q := request{s: s, project: project}
		if rid := str(r["id"]); rid != "" {
			for _, bucket := range s.state.Objects {
				if current := bucket[rid]; current != nil && recordExpired(current) {
					r["expiresAt"] = current["expiresAt"]
					break
				}
			}
		}
		if dependentBody(r) && q.hasExpiredRefs(r, map[string]bool{}) {
			r["expiresAt"] = time.Now().Add(-time.Nanosecond).UTC().Format(time.RFC3339Nano)
		}
		for key, v := range r {
			r[key] = s.scrubExpiredCopies(v, project)
		}
		return scrubExpired(r)
	case []any:
		for i, v := range r {
			r[i] = s.scrubExpiredCopies(v, project)
		}
		return r
	case []Record:
		for i, v := range r {
			r[i] = s.scrubExpiredCopies(v, project).(Record)
		}
		return r
	}
	return value
}
