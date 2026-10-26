package workbench

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"
)

var pageDiagnosticNamespace = regexp.MustCompile(`"schema"\s*:\s*"xlb\.page-diagnostic"`)

var pageDiagnosticID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var pageDiagnosticEnums = map[string][]string{
	"pageName":  {"MiniApp"},
	"eventName": {"page_open_started", "bundle_load_started", "bundle_load_succeeded", "bundle_load_failed", "rn_mounted", "content_ready", "page_interactive", "render_failed", "page_suspected_blank", "page_slow", "page_abandoned"},
	"stage":     {"page", "bundle", "render", "content", "interactive"},
	"outcome":   {"started", "succeeded", "failed", "suspected", "abandoned"},
	"source":    {"native", "rn"}, "level": {"info", "warn", "error"}, "module": {"page", "bundle", "render", "network"},
	"scenario":  {"normal", "load_failure", "render_failure", "blank", "slow"},
	"dataState": {"content", "empty", "error", "unknown"},
}

// normalizePageDiagnostic promotes only the versioned, allowlisted content contract.
// The envelope and content remain untouched as evidence even when validation fails.
func normalizePageDiagnostic(ev Record) {
	var payload map[string]any
	content := str(ev["content"])
	if json.Unmarshal([]byte(content), &payload) != nil {
		if strings.HasPrefix(strings.TrimSpace(content), "{") && pageDiagnosticNamespace.MatchString(content) {
			ev["diagnosticWarning"] = "PAGE_DIAGNOSTIC_INVALID: JSON"
		}
		return
	}
	if payload["schema"] != "xlb.page-diagnostic" {
		return
	}
	ev["envelopeTime"] = ev["rawTime"]
	if nativeTime, present := record(ev["fields"])["envelopeTime"]; present {
		ev["envelopeTime"] = nativeTime
	}
	if line, ok := record(ev["fields"])["originalLine"]; ok {
		ev["originalLine"] = line
	}
	invalid := func(field string) { ev["diagnosticWarning"] = "PAGE_DIAGNOSTIC_INVALID: " + field }
	if payload["schemaVersion"] != float64(1) {
		invalid("schemaVersion")
		return
	}
	for _, key := range []string{"eventId", "pageAttemptId", "retryOf", "errorCode"} {
		value, present := payload[key]
		if !present && (key == "retryOf" || key == "errorCode") {
			continue
		}
		text, ok := value.(string)
		if !ok || !pageDiagnosticID.MatchString(text) {
			invalid(key)
			return
		}
	}
	for key, options := range pageDiagnosticEnums {
		value, present := payload[key]
		if !present && (key == "scenario" || key == "dataState") {
			continue
		}
		text, ok := value.(string)
		if !ok || !contains(options, text) {
			invalid(key)
			return
		}
	}
	// Millisecond epoch only: exclude second-scale values and dates beyond RFC3339.
	epoch, ok := payload["occurredAtMs"].(float64)
	if !ok || math.IsNaN(epoch) || math.IsInf(epoch, 0) || math.Trunc(epoch) != epoch || epoch < 100000000000 || epoch > 253402300799999 {
		invalid("occurredAtMs")
		return
	}
	for _, key := range []string{"monotonicMs", "durationMs"} {
		value, present := payload[key]
		if !present && key == "durationMs" {
			continue
		}
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
			invalid(key)
			return
		}
	}
	if value, present := payload["testInjected"]; present {
		if _, ok := value.(bool); !ok {
			invalid("testInjected")
			return
		}
	}
	for key := range payload {
		switch key {
		case "schema", "schemaVersion", "eventId", "pageAttemptId", "retryOf", "errorCode", "occurredAtMs", "monotonicMs", "durationMs", "testInjected":
		default:
			if _, ok := pageDiagnosticEnums[key]; !ok {
				invalid("unexpected field")
				return
			}
		}
	}
	for key, value := range payload {
		ev[key] = value
	}
	ev["time"] = time.UnixMilli(int64(epoch)).UTC().Format(time.RFC3339Nano)
}
