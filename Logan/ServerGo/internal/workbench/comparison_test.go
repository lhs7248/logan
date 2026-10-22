package workbench

import "testing"

func TestParseComparisonKeepsPublishedVersionsAndDiagnosesFailedStrictRun(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	asset := upload(t, s, "p", "comparison.jsonl", "{\"time\":\"2026-10-21T10:00:00Z\",\"message\":\"one\"}\nbad-json\n{\"message\":\"two\"}\n")
	_, result := req(t, s, "POST", "/assets/"+asset+"/parse-runs", "p", "comparison-strict", "", Record{"format": "jsonl", "mode": "strict"})
	left := data(t, result)["id"].(string)
	awaitRun(t, s, left)
	_, result = req(t, s, "POST", "/assets/"+asset+"/parse-runs", "p", "comparison-skip", "", Record{"format": "jsonl", "mode": "skip_invalid_records"})
	right := data(t, result)["id"].(string)
	awaitRun(t, s, right)
	status, result := req(t, s, "GET", "/parse-runs/"+left+"/comparison?otherRunId="+right, "p", "", "", nil)
	if status != 200 {
		t.Fatalf("compare status=%d result=%v", status, result)
	}
	d := data(t, result)
	if d["sameAsset"] != true || d["addedCount"] != float64(2) || d["removedCount"] != float64(0) {
		t.Fatalf("comparison lost strict-versus-partial publication distinction: %v", d)
	}
	_, after := req(t, s, "GET", "/parse-runs/"+left, "p", "", "", nil)
	if data(t, after)["status"] != "failed" || data(t, after)["eventCount"] != float64(0) {
		t.Fatal("comparison modified the old parse run")
	}
	otherAsset := upload(t, s, "p", "other-comparison.jsonl", "{\"message\":\"different original\"}\n")
	_, result = req(t, s, "POST", "/assets/"+otherAsset+"/parse-runs", "p", "other-comparison", "", Record{})
	other := data(t, result)["id"].(string)
	awaitRun(t, s, other)
	status, _ = req(t, s, "GET", "/parse-runs/"+left+"/comparison?otherRunId="+other, "p", "", "", nil)
	if status != 422 {
		t.Fatalf("different originals were incorrectly compared as parse versions: %d", status)
	}
}
