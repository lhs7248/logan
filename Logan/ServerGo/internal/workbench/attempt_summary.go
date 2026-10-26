package workbench

// attemptSummaries consumes the complete authorized parse run, independent
// of event-page size. Only normalized diagnostics participate; raw content is
// neither reparsed nor included in this compact navigation response.
func attemptSummaries(events []Record) []Record {
	type progress struct {
		result                       Record
		seen                         map[string]bool
		ready, interactive, terminal bool
	}
	groups := map[string]*progress{}
	ordered := []*progress{}
	for _, e := range events {
		if str(e["schema"]) != "xlb.page-diagnostic" || num(e["schemaVersion"]) != 1 || str(e["pageAttemptId"]) == "" {
			continue
		}
		id := str(e["pageAttemptId"])
		g := groups[id]
		if g == nil {
			g = &progress{result: Record{"runId": e["runId"], "pageAttemptId": id, "pageName": e["pageName"], "retryOf": e["retryOf"], "status": "incomplete", "eventCount": 0, "events": []Record{}, "slow": false, "testInjected": false, "hasErrors": false, "durationMs": nil, "scenarios": []string{}}, seen: map[string]bool{}}
			groups[id] = g
			ordered = append(ordered, g)
		}
		r := g.result
		name := str(e["eventName"])
		g.seen[name] = true
		r["eventCount"] = num(r["eventCount"]) + 1
		if name == "page_slow" {
			r["slow"] = true
		}
		if e["testInjected"] == true {
			r["testInjected"] = true
		}
		if scenario := str(e["scenario"]); scenario != "" {
			key := "scenario:" + scenario
			if !g.seen[key] {
				r["scenarios"] = append(r["scenarios"].([]string), scenario)
				g.seen[key] = true
			}
		}
		if duration, ok := e["durationMs"].(float64); ok {
			previous, exists := r["durationMs"].(float64)
			if !exists || duration > previous {
				r["durationMs"] = duration
			}
		}
		failed := name == "bundle_load_failed" || name == "render_failed"
		if failed {
			r["hasErrors"] = true
		}
		if !g.terminal {
			switch {
			case failed:
				r["status"] = "failed"
				g.terminal = true
			case name == "page_abandoned":
				r["status"] = "abandoned"
				g.terminal = true
			default:
				g.ready = g.ready || name == "content_ready"
				g.interactive = g.interactive || name == "page_interactive"
				if name == "page_suspected_blank" {
					r["status"] = "suspected"
				}
				if g.ready && g.interactive {
					r["status"] = "succeeded"
					g.terminal = true
				}
			}
		}
		stage := Record{}
		for _, k := range []string{"id", "runId", "schema", "schemaVersion", "pageAttemptId", "pageName", "eventName", "stage", "outcome", "source", "time", "occurredAtMs", "monotonicMs", "errorCode", "durationMs", "testInjected"} {
			if e[k] != nil {
				stage[k] = e[k]
			}
		}
		stages := r["events"].([]Record)
		if len(stages) == 48 {
			stages = append(stages[:24], stages[25:]...)
			r["stagesTruncated"] = true
		}
		r["events"] = append(stages, stage)
	}
	out := []Record{}
	for _, g := range ordered {
		gaps := []string{}
		for _, key := range []string{"page_open_started", "content_ready", "page_interactive"} {
			if !g.seen[key] {
				gaps = append(gaps, "完整批次未记录 "+key+" 证据")
			}
		}
		g.result["gaps"] = gaps
		out = append(out, g.result)
	}
	return out
}
