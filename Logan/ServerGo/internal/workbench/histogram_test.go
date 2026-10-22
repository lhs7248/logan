package workbench

import (
	"testing"
)

func TestAdaptiveHistogramPreservesFilteredEventsErrorsAndPreciseRange(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	a := upload(t, s, "p", "short-window.jsonl", "{\"time\":\"2026-10-19T10:42:01.120+08:00\",\"level\":\"INFO\",\"content\":\"start\"}\n{\"time\":\"2026-10-19T10:42:09.142+08:00\",\"level\":\"ERROR\",\"content\":\"timeout\"}\n{\"time\":\"2026-10-19T10:42:25.162+08:00\",\"level\":\"FATAL\",\"content\":\"failed\"}\n{\"content\":\"unknown time\"}\n")
	_, o := req(t, s, "POST", "/assets/"+a+"/parse-runs", "p", "adaptive-parse", "", Record{"format": "jsonl", "mode": "strict"})
	r := str(data(t, o)["id"])
	awaitRun(t, s, r)
	code, o := req(t, s, "GET", "/parse-runs/"+r+"/histogram?bins=24", "p", "", "", nil)
	h := data(t, o)
	if code != 200 || len(sliceOf(h["buckets"])) != 24 {
		t.Fatal("short window still collapsed to a minute", o)
	}
	count, errors := 0, 0
	for _, v := range sliceOf(h["buckets"]) {
		b := record(v)
		count += num(b["count"])
		errors += num(b["errors"])
		if num(b["count"]) > 0 && str(b["firstEventId"]) == "" {
			t.Fatal("nonempty bin lacks exact event locator")
		}
	}
	if count != 3 || errors != 2 || num(h["unknownTime"]) != 1 || h["rangeStart"] != "2026-10-19T02:42:01.12Z" || h["rangeEnd"] != "2026-10-19T02:42:25.162Z" {
		t.Fatal(h)
	}
	_, o = req(t, s, "GET", "/parse-runs/"+r+"/events?level=ERROR", "p", "", "", nil)
	expected := data(t, o)
	_, o = req(t, s, "GET", "/parse-runs/"+r+"/histogram?level=ERROR&bins=24", "p", "", "", nil)
	if data(t, o)["total"] != expected["total"] || data(t, o)["filterHash"] != expected["filterHash"] {
		t.Fatal("adaptive histogram changed filter semantics")
	}
}
