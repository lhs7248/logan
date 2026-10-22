package workbench

import (
	"fmt"
	"strings"
	"testing"
)

// Equal and unknown timestamps retain file order across pages, context and exports.
func TestLogReadingPreservesOriginalOrderForEqualAndUnknownTimes(t *testing.T) {
	for _, timestamp := range []string{"2026-10-22T10:00:00Z", ""} {
		t.Run("timestamp="+timestamp, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			defer s.Close()
			var raw strings.Builder
			for i := 1; i <= 12; i++ {
				fmt.Fprintf(&raw, "{\"time\":%q,\"content\":\"record %d\"}\n", timestamp, i)
			}
			asset := upload(t, s, "p", "ordered.jsonl", raw.String())
			_, o := req(t, s, "POST", "/assets/"+asset+"/parse-runs", "p", "parse-order", "", Record{"format": "jsonl"})
			run := str(data(t, o)["id"])
			awaitRun(t, s, run)
			cursor := ""
			line := 1
			for {
				_, o = req(t, s, "GET", "/parse-runs/"+run+"/events?limit=5&cursor="+cursor, "p", "", "", nil)
				page := data(t, o)
				for _, v := range sliceOf(page["events"]) {
					if num(record(v)["line"]) != line {
						t.Fatalf("position %d returned line %v", line, record(v)["line"])
					}
					line++
				}
				cursor = str(page["nextCursor"])
				if cursor == "" {
					break
				}
			}
			if line != 13 {
				t.Fatalf("lost records: next=%d", line)
			}
			_, o = req(t, s, "GET", "/events/"+run+"-6/context", "p", "", "", nil)
			for i, v := range sliceOf(data(t, o)["events"]) {
				if num(record(v)["line"]) != i+1 {
					t.Fatal("context reordered", o)
				}
			}
			_, o = req(t, s, "POST", "/exports", "p", "export-order", "", Record{"kind": "events", "runId": run})
			exp := str(data(t, o)["id"])
			s.mu.Lock()
			payload := record(s.state.bucket("exports")[exp]["payload"])
			for i, v := range sliceOf(payload["events"]) {
				if num(record(v)["line"]) != i+1 {
					s.mu.Unlock()
					t.Fatal("export reordered")
				}
			}
			s.mu.Unlock()
		})
	}
}
