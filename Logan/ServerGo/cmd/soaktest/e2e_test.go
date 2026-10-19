package main

import (
	"testing"
	"time"
)

func TestE2EFailureAccountingAndSeparateFlows(t *testing.T) {
	s := newStats()
	for i := 1; i <= 100; i++ {
		s.recordE2E("native", ledger{Cycle: i}, time.Now(), time.Duration(i)*time.Millisecond)
	}
	s.recordE2E("web", ledger{Cycle: 1, Classification: "timeout"}, time.Now().Add(-time.Second), 0)
	r := s.e2eReports(true)
	if r["native"].CompletedObservations != 100 || r["native"].Censored != 0 || *r["native"].P95MS != 95 || *r["native"].P99MS != 99 {
		t.Fatalf("wrong native percentiles: %+v", r["native"])
	}
	web := r["web"]
	if web.CompletedObservations != 1 || web.Censored != 1 || web.Queryable != 0 || web.P95MS != nil || web.SuccessP95MS != nil || web.Samples[0].ElapsedMS < 1000 {
		t.Fatalf("failed web observation excluded or treated as completion: %+v", web)
	}
	s.recordE2E("native", ledger{Classification: "detail_identity"}, time.Now(), 0)
	native := s.e2eReports(true)["native"]
	if native.CompletedObservations != 101 || native.Censored != 1 || native.P95MS != nil || native.P99MS != nil || *native.SuccessP95MS != 95 {
		t.Fatalf("censoring hidden by successful samples: %+v", native)
	}
}

func TestE2EIntermediatePercentilesAreConditional(t *testing.T) {
	s := newStats()
	s.recordE2E("native", ledger{}, time.Now(), time.Millisecond)
	e := &engine{stats: s}
	r := e.snapshot(false)["upload_to_all_details"].(map[string]e2eReport)["native"]
	if r.P95MS != nil || r.P99MS != nil || r.SuccessP95MS == nil {
		t.Fatalf("intermediate completed observations presented as unconditional: %+v", r)
	}
}

func TestE2EActiveUploadBlocksFinalPercentiles(t *testing.T) {
	s := newStats()
	s.recordE2E("native", ledger{}, time.Now(), time.Millisecond)
	start := s.beginE2E("native")
	e := &engine{stats: s}
	r := e.snapshot(true)["upload_to_all_details"].(map[string]e2eReport)["native"]
	if r.ActiveUploads != 1 || r.CompletedObservations != 1 || r.P95MS != nil || r.P99MS != nil {
		t.Fatalf("active upload missing from final accounting: %+v", r)
	}
	s.recordE2E("native", ledger{Classification: "download_content"}, start, 2*time.Millisecond)
	r = e.snapshot(true)["upload_to_all_details"].(map[string]e2eReport)["native"]
	if r.ActiveUploads != 0 || r.CompletedObservations != 2 || r.Censored != 0 || r.P95MS == nil {
		t.Fatalf("later workflow failure censored successful milestone: %+v", r)
	}
}
