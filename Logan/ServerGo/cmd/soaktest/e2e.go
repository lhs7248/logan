package main

import "time"

// ElapsedMS ends at the verified detail milestone, or at failure observation.
// A censored sample is not a successful latency and has no assumed completion.
type e2eSample struct {
	Cycle     int
	TaskID    int64
	Queryable bool
	ElapsedMS float64
	Failure   string
}
type e2eReport struct {
	Samples                                                   []e2eSample
	CompletedObservations, ActiveUploads, Queryable, Censored int
	Final                                                     bool
	P95MS, P99MS                                              *float64
	SuccessP95MS, SuccessP99MS                                *float64
	Accounting                                                string
}

func (s *stats) beginE2E(flow string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.E2EActive == nil {
		s.E2EActive = map[string]int{}
	}
	s.E2EActive[flow]++
	return time.Now()
}

func (s *stats) recordE2E(flow string, l ledger, start time.Time, reached time.Duration) {
	elapsed := reached
	if elapsed == 0 {
		elapsed = time.Since(start)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.E2EActive[flow] > 0 {
		s.E2EActive[flow]--
	}
	if s.E2E == nil {
		s.E2E = map[string][]e2eSample{}
	}
	s.E2E[flow] = append(s.E2E[flow], e2eSample{
		Cycle: l.Cycle, TaskID: l.TaskID, Queryable: reached > 0,
		ElapsedMS: float64(elapsed) / float64(time.Millisecond), Failure: l.Classification,
	})
}

// Called under stats.mu by snapshot.
func (s *stats) e2eReports(final bool) map[string]e2eReport {
	out := map[string]e2eReport{}
	for _, flow := range []string{"native", "web"} {
		r := e2eReport{Final: final, ActiveUploads: s.E2EActive[flow], Samples: append([]e2eSample{}, s.E2E[flow]...),
			Accounting: "monotonic wall elapsed from immediately before upload call through all expected detail IDs and contents verified; includes intervening route checks and polling; failure before milestone is censored at observation, not completion; samples and CompletedObservations include only finished workflows; ActiveUploads counts ongoing uploaded workflows even after milestone; unconditional P95/P99 null for intermediate snapshots, active uploads, any censored, or no samples; success percentiles conditional; dropped/unstarted arrivals reported separately"}
		success := []float64{}
		for _, v := range r.Samples {
			r.CompletedObservations++
			if v.Queryable {
				r.Queryable++
				success = append(success, v.ElapsedMS)
			} else {
				r.Censored++
			}
		}
		if len(success) > 0 {
			p95, p99 := percentile(success, .95), percentile(success, .99)
			r.SuccessP95MS, r.SuccessP99MS = &p95, &p99
			if final && r.ActiveUploads == 0 && r.Censored == 0 {
				r.P95MS, r.P99MS = &p95, &p99
			}
		}
		out[flow] = r
	}
	return out
}
