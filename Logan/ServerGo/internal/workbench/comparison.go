package workbench

import (
	"encoding/json"
	"net/url"
	"sort"
)

// compareRuns compares immutable publications from the same original asset.
// A strict failed publication stays empty; a later readable run never repairs or
// renumbers it. Results contain references and hashes, not duplicated log text.
func (q *request) compareRuns(left Record) (any, int, *apiError) {
	right, err := q.get("parseRuns", q.r.URL.Query().Get("otherRunId"))
	if err != nil {
		return nil, 0, err
	}
	if left["assetId"] != right["assetId"] {
		return nil, 0, fail(422, "DIFFERENT_SOURCE", "Compare parse versions of the same original asset")
	}
	for _, run := range []Record{left, right} {
		if run["status"] == "queued" || run["status"] == "running" {
			return nil, 0, fail(409, "PARSE_ACTIVE", "Wait for both parse attempts to finish")
		}
	}
	l, _, err := q.filtered(left, url.Values{})
	if err != nil {
		return nil, 0, err
	}
	r, _, err := q.filtered(right, url.Values{})
	if err != nil {
		return nil, 0, err
	}
	index := func(events []Record) map[int]Record {
		out := map[int]Record{}
		for _, event := range events {
			out[num(event["line"])] = event
		}
		return out
	}
	hash := func(event Record) string {
		if event == nil {
			return ""
		}
		body, _ := json.Marshal(Record{"time": event["time"], "level": event["level"], "module": event["module"], "content": event["content"], "fields": event["fields"]})
		return digest(body)
	}
	a, b := index(l), index(r)
	positions := map[int]bool{}
	for position := range a {
		positions[position] = true
	}
	for position := range b {
		positions[position] = true
	}
	lines := make([]int, 0, len(positions))
	for line := range positions {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	added, removed, changed, unchanged := 0, 0, 0, 0
	differences := []Record{}
	for _, line := range lines {
		before, after := a[line], b[line]
		kind := ""
		switch {
		case before == nil:
			added++
			kind = "added"
		case after == nil:
			removed++
			kind = "removed"
		case hash(before) != hash(after):
			changed++
			kind = "changed"
		default:
			unchanged++
		}
		if kind != "" && len(differences) < 1000 {
			var beforeID, afterID any
			if before != nil {
				beforeID = before["id"]
			}
			if after != nil {
				afterID = after["id"]
			}
			differences = append(differences, Record{"line": line, "kind": kind, "leftEventId": beforeID, "rightEventId": afterID, "leftHash": hash(before), "rightHash": hash(after)})
		}
	}
	summary := func(run Record) Record {
		return Record{"id": run["id"], "status": run["status"], "format": run["format"], "mode": run["mode"], "parserVersion": run["parserVersion"], "eventCount": run["eventCount"], "errorCount": run["errorCount"], "errors": run["errors"]}
	}
	return Record{"assetId": left["assetId"], "sameAsset": true, "left": summary(left), "right": summary(right), "addedCount": added, "removedCount": removed, "changedCount": changed, "unchangedCount": unchanged, "differences": differences, "truncated": len(differences) < added+removed+changed}, 200, nil
}
