package workbench

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func diagnosticFixture() Record {
	return Record{"schema": "xlb.page-diagnostic", "schemaVersion": 1, "eventId": "event-1", "pageAttemptId": "attempt_1", "pageName": "MiniApp", "eventName": "page_open_started", "stage": "page", "outcome": "started", "occurredAtMs": 1792980000123, "monotonicMs": 12.5, "source": "rn", "level": "info", "module": "page"}
}
func parseDiagnosticFixture(t *testing.T, payload Record) Record {
	t.Helper()
	content, _ := json.Marshal(payload)
	rows, _ := json.Marshal([]Record{{"content": string(content), "time": "2026-10-26T04:00:00+08:00", "logType": 7, "originalLine": 42, "requestId": "outer-request"}})
	events, bad, fatal := parseRecords(context.Background(), rows, "json", "run", "project", 100)
	if fatal != "" || len(bad) != 0 || len(events) != 1 {
		t.Fatalf("parse: %v %v %v", events, bad, fatal)
	}
	return events[0]
}
func TestPageDiagnosticNormalizesContentAndPreservesEnvelope(t *testing.T) {
	p := diagnosticFixture()
	for key, value := range (Record{"durationMs": 500.5, "errorCode": "BUNDLE_UNAVAILABLE", "retryOf": "attempt-0", "scenario": "slow", "testInjected": true, "dataState": "unknown"}) {
		p[key] = value
	}
	ev := parseDiagnosticFixture(t, p)
	for key, value := range p {
		if !reflect.DeepEqual(ev[key], value) && !(key == "schemaVersion" || key == "occurredAtMs") {
			t.Errorf("%s = %#v; want %#v", key, ev[key], value)
		}
	}
	if ev["time"] != "2026-10-26T02:00:00.123Z" {
		t.Errorf("occurred time = %v", ev["time"])
	}
	for key, value := range (Record{"envelopeTime": "2026-10-26T04:00:00+08:00", "rawTime": "2026-10-26T04:00:00+08:00", "originalLine": float64(42), "requestId": "outer-request", "logType": float64(7), "id": "run-1", "runId": "run"}) {
		if !reflect.DeepEqual(ev[key], value) {
			t.Errorf("%s = %#v; want %#v", key, ev[key], value)
		}
	}
	fields := record(ev["fields"])
	if fields["content"] != ev["content"] || fields["time"] != "2026-10-26T04:00:00+08:00" {
		t.Fatalf("original fields lost: %v", ev)
	}
	if ev["diagnosticWarning"] != nil {
		t.Fatalf("unexpected warning: %v", ev)
	}
}
func TestPageDiagnosticRejectsInvalidKnownNamespaceWithoutLosingRaw(t *testing.T) {
	cases := []struct {
		name, key string
		value     any
	}{
		{"version", "schemaVersion", 2}, {"string version", "schemaVersion", "1"}, {"missing id", "eventId", nil}, {"unsafe id", "pageAttemptId", "https://private"}, {"long id", "eventId", strings.Repeat("a", 129)},
		{"page", "pageName", "Other"}, {"event", "eventName", "request_completed"}, {"stage", "stage", "network"}, {"outcome", "outcome", "success"}, {"source", "source", "web"}, {"level", "level", "debug"}, {"module", "module", "auth"},
		{"seconds", "occurredAtMs", 1792980000}, {"fractional epoch", "occurredAtMs", 1792980000123.5}, {"overflow epoch", "occurredAtMs", 1e20}, {"string epoch", "occurredAtMs", "1792980000123"}, {"negative monotonic", "monotonicMs", -1}, {"missing monotonic", "monotonicMs", nil},
		{"duration", "durationMs", -1}, {"bool", "testInjected", "true"}, {"scenario", "scenario", "random"}, {"data", "dataState", "loaded"}, {"retry", "retryOf", "unsafe id"}, {"error", "errorCode", "secret message"}, {"unknown sensitive field", "url", "https://secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := diagnosticFixture()
			if tc.value == nil {
				delete(p, tc.key)
			} else {
				p[tc.key] = tc.value
			}
			ev := parseDiagnosticFixture(t, p)
			if str(ev["diagnosticWarning"]) == "" {
				t.Fatalf("invalid accepted: %v", ev)
			}
			if ev["pageAttemptId"] != nil || ev["schema"] != nil {
				t.Fatalf("invalid promoted: %v", ev)
			}
			if ev["time"] != "2026-10-25T20:00:00Z" || ev["content"] != record(ev["fields"])["content"] {
				t.Fatalf("raw changed: %v", ev)
			}
		})
	}
}
func TestPageDiagnosticExactEnums(t *testing.T) {
	values := map[string][]string{"eventName": {"page_open_started", "bundle_load_started", "bundle_load_succeeded", "bundle_load_failed", "rn_mounted", "content_ready", "page_interactive", "render_failed", "page_suspected_blank", "page_slow", "page_abandoned"}, "stage": {"page", "bundle", "render", "content", "interactive"}, "outcome": {"started", "succeeded", "failed", "suspected", "abandoned"}, "source": {"native", "rn"}, "level": {"info", "warn", "error"}, "module": {"page", "bundle", "render", "network"}, "scenario": {"normal", "load_failure", "render_failure", "blank", "slow"}, "dataState": {"content", "empty", "error", "unknown"}}
	for key, options := range values {
		for _, value := range options {
			t.Run(key+"/"+value, func(t *testing.T) {
				p := diagnosticFixture()
				p[key] = value
				ev := parseDiagnosticFixture(t, p)
				if ev[key] != value || ev["diagnosticWarning"] != nil {
					t.Fatalf("valid value rejected: %v", ev)
				}
			})
		}
	}
}
func TestPageDiagnosticLegacyAndForeignContentUnchanged(t *testing.T) {
	for _, content := range []string{"legacy text", `{"eventName":"page_open_started","level":"error"}`, `{"schema":"other.page-diagnostic","schemaVersion":1}`, `{"schema":"xlb.page-diagnostic-bogus","schemaVersion":1}`} {
		raw, _ := json.Marshal([]Record{{"content": content, "level": "WARN", "module": "legacy", "time": "2026-10-26T00:00:00Z", "requestId": "real-request"}})
		events, bad, fatal := parseRecords(context.Background(), raw, "json", "run", "project", 100)
		if fatal != "" || len(bad) != 0 || len(events) != 1 {
			t.Fatal("legacy parse failed")
		}
		ev := events[0]
		if ev["content"] != content || ev["level"] != "warn" || ev["module"] != "legacy" || ev["requestId"] != "real-request" || ev["diagnosticWarning"] != nil || ev["pageAttemptId"] != nil {
			t.Fatalf("legacy changed: %v", ev)
		}
	}
}

func TestPageDiagnosticPreservesNativeEnvelopeAndDoesNotForgeRequestID(t *testing.T) {
	content, _ := json.Marshal(diagnosticFixture())
	raw, _ := json.Marshal([]Record{{"content": string(content), "time": "2026-10-26T00:00:00Z", "envelopeTime": 1792972800, "originalLine": 19}})
	events, _, _ := parseRecords(context.Background(), raw, "json", "run", "project", 100)
	ev := events[0]
	if ev["envelopeTime"] != float64(1792972800) || ev["requestId"] != "" {
		t.Fatalf("native envelope/id changed: %v", ev)
	}
}

func TestPageDiagnosticMixedLegacyAndDiagnosticRecords(t *testing.T) {
	content, _ := json.Marshal(diagnosticFixture())
	raw, _ := json.Marshal([]Record{{"content": "old text", "level": "warn"}, {"content": string(content)}, {"content": `{"schema":"xlb.page-diagnostic","schemaVersion":99}`}, {"content": "last legacy"}})
	events, bad, fatal := parseRecords(context.Background(), raw, "json", "run", "project", 100)
	if fatal != "" || len(bad) != 0 || len(events) != 4 {
		t.Fatalf("mixed parse failed: %v %v %s", events, bad, fatal)
	}
	if events[0]["content"] != "old text" || events[1]["pageAttemptId"] != "attempt_1" || str(events[2]["diagnosticWarning"]) == "" || events[3]["content"] != "last legacy" {
		t.Fatalf("mixed behavior: %v", events)
	}
}

func TestPageDiagnosticMalformedKnownNamespaceWarns(t *testing.T) {
	raw, _ := json.Marshal([]Record{{"content": `{"schema" : "xlb.page-diagnostic", "schemaVersion": 1,`, "time": "2026-10-26T00:00:00Z"}})
	events, bad, fatal := parseRecords(context.Background(), raw, "json", "run", "project", 100)
	if fatal != "" || len(bad) != 0 || len(events) != 1 || str(events[0]["diagnosticWarning"]) == "" {
		t.Fatalf("missing malformed diagnostic warning: %v %v %s", events, bad, fatal)
	}
	if events[0]["pageAttemptId"] != nil || events[0]["content"] != record(events[0]["fields"])["content"] {
		t.Fatal("malformed diagnostic was promoted or lost")
	}
}
