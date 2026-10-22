package workbench

import (
	"strings"
	"time"
)

// adaptiveHistogram keeps short incident windows useful while preserving the
// exact server-side filtered population. Default minute aggregation stays intact.
func (q *request) adaptiveHistogram(run Record, events []Record, hash string) Record {
	const bins = 24
	var first, last time.Time
	unknown := 0
	for _, event := range events {
		at, err := time.Parse(time.RFC3339Nano, str(event["time"]))
		if err != nil {
			unknown++
			continue
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if last.IsZero() || at.After(last) {
			last = at
		}
	}
	buckets := []Record{}
	result := Record{"buckets": buckets, "unknownTime": unknown, "total": len(events), "filterHash": hash, "runId": run["id"]}
	if first.IsZero() {
		return result
	}
	width := (last.UnixMilli() - first.UnixMilli() + bins) / bins
	if width < 1 {
		width = 1
	}
	for i := int64(0); i < bins; i++ {
		buckets = append(buckets, Record{"time": time.UnixMilli(first.UnixMilli() + i*width).UTC().Format(time.RFC3339Nano), "count": 0, "errors": 0})
	}
	for _, event := range events {
		at, err := time.Parse(time.RFC3339Nano, str(event["time"]))
		if err != nil {
			continue
		}
		index := (at.UnixMilli() - first.UnixMilli()) / width
		if index >= bins {
			index = bins - 1
		}
		if index < 0 {
			index = 0
		}
		b := buckets[index]
		b["count"] = num(b["count"]) + 1
		if str(b["firstEventId"]) == "" {
			b["firstEventId"] = event["id"]
		}
		if contains([]string{"ERROR", "FATAL"}, strings.ToUpper(str(event["level"]))) {
			b["errors"] = num(b["errors"]) + 1
		}
	}
	result["buckets"] = buckets
	result["rangeStart"] = first.UTC().Format(time.RFC3339Nano)
	result["rangeEnd"] = last.UTC().Format(time.RFC3339Nano)
	return result
}
