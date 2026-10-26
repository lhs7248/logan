package workbench

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPageAttemptScopeMatchesEventsHistogramAndExport(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	var lines []string
	for i := 0; i < 260; i++ {
		p := diagnosticFixture()
		p["eventId"] = fmt.Sprintf("event-%d", i)
		p["occurredAtMs"] = 1792980000123 + i
		if i == 258 {
			p["eventName"] = "content_ready"
		}
		if i == 259 {
			p["eventName"] = "page_interactive"
		}
		b, _ := json.Marshal(p)
		row, _ := json.Marshal(Record{"content": string(b)})
		lines = append(lines, string(row))
	}
	p := diagnosticFixture()
	p["pageAttemptId"] = "another"
	b, _ := json.Marshal(p)
	row, _ := json.Marshal(Record{"content": string(b)})
	lines = append(lines, string(row))
	a := upload(t, s, "p", "attempts.jsonl", strings.Join(lines, "\n"))
	_, out := req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "scope-parse", "", Record{"format": "jsonl", "mode": "strict"})
	r := str(data(t, out)["id"])
	awaitRun(t, s, r)
	_, out = req(t, s, "GET", "/parse-runs/"+r+"/events?pageAttemptId=attempt_1&limit=200", "p", "", "", nil)
	events := data(t, out)
	if num(events["total"]) != 260 {
		t.Fatalf("attempt scope leaked: %v", events["total"])
	}
	_, out = req(t, s, "GET", "/parse-runs/"+r+"/histogram?pageAttemptId=attempt_1&bins=24", "p", "", "", nil)
	if data(t, out)["filterHash"] != events["filterHash"] || num(data(t, out)["total"]) != 260 {
		t.Fatal(out)
	}
	code, out := req(t, s, "GET", "/parse-runs/"+r+"/page-attempts", "p", "", "", nil)
	if code != 200 {
		t.Fatalf("complete summary unavailable: %d %v", code, out)
	}
	attempts := sliceOf(data(t, out)["attempts"])
	if len(attempts) != 2 {
		t.Fatal(out)
	}
	first := record(attempts[0])
	if first["status"] != "succeeded" || num(first["eventCount"]) != 260 {
		t.Fatal(first)
	}
	if len(sliceOf(first["events"])) > 48 || first["stagesTruncated"] != true {
		t.Fatal("stage list must be bounded", first)
	}
	_, filteredOut := req(t, s, "GET", "/parse-runs/"+r+"/page-attempts?level=ERROR", "p", "", "", nil)
	complete := data(t, filteredOut)
	if len(sliceOf(complete["attempts"])) != 2 || record(sliceOf(complete["attempts"])[0])["status"] != "succeeded" || num(record(sliceOf(complete["attempts"])[0])["matchingEventCount"]) != 0 || complete["summaryScope"] != "entire_run" {
		t.Fatal("filter changed the complete attempt conclusion", complete)
	}
	code, out = req(t, s, "POST", "/exports", "p", "attempt-export", "", Record{"kind": "events", "runId": r, "pageAttemptId": "attempt_1"})
	if code != 201 {
		t.Fatal(code, out)
	}
	exportID := str(data(t, out)["id"])
	// Export content has a raw response, so inspect it using the durable saved export.
	s.mu.Lock()
	exp := clone(s.state.bucket("exports")[exportID])
	s.mu.Unlock()
	if exp["id"] == nil {
		t.Fatal("export not saved")
	}
	payload := record(exp["payload"])
	if num(payload["total"]) != 260 || payload["filterHash"] != events["filterHash"] {
		t.Fatal("export scope diverged", payload["total"])
	}
	_, other := req(t, s, "GET", "/parse-runs/"+r+"/events?cursor="+str(events["nextCursor"])+"&pageAttemptId=another", "p", "", "", nil)
	if record(other["error"])["code"] != "CURSOR_INVALID" {
		t.Fatal("cursor crossed attempts", other)
	}
}

func TestAttemptSummaryKeepsTerminalFactsAndPreciseReportedDuration(t *testing.T) {
	for _, tc := range []struct{ first, want string }{{"bundle_load_failed", "failed"}, {"page_abandoned", "abandoned"}, {"page_suspected_blank", "succeeded"}} {
		var events []Record
		for i, name := range []string{"page_open_started", tc.first, "content_ready", "page_interactive"} {
			e := diagnosticFixture()
			e["eventName"] = name
			e["id"] = fmt.Sprint(i)
			e["runId"] = "r"
			e["durationMs"] = 1.5 + float64(i)/10
			events = append(events, e)
		}
		rows := attemptSummaries(events)
		if len(rows) != 1 || rows[0]["status"] != tc.want || rows[0]["durationMs"] != 1.8 {
			t.Fatal(tc, rows)
		}
		for _, e := range rows[0]["events"].([]Record) {
			if e["content"] != nil || e["fields"] != nil {
				t.Fatal("summary leaked raw bodies")
			}
		}
	}
	if len(attemptSummaries([]Record{{"content": `{"schema":"xlb.page-diagnostic","pageAttemptId":"fake"}`}})) != 0 {
		t.Fatal("raw text became diagnostic evidence")
	}
}
