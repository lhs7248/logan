package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const demoContent = `{"time":"2026-10-21T10:00:00Z","level":"info","module":"network","content":"Synthetic request started","requestId":"demo-request"}
{"time":"2026-10-21T10:00:01Z","level":"error","module":"network","content":"Synthetic request timed out","requestId":"demo-request"}
{"level":"warn","content":"Synthetic UI state observation; build unknown"}
`

func (q *request) importFiles() (any, int, *apiError) {
	_, params, err := mime.ParseMediaType(q.r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return nil, 0, fail(400, "MULTIPART_REQUIRED", "Upload metadata and repeated files parts")
	}
	mr := multipart.NewReader(bytes.NewReader(q.body), params["boundary"])
	meta := Record{}
	type file struct {
		name string
		raw  []byte
	}
	files := []file{}
	total := int64(0)
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, fail(400, "MULTIPART_INVALID", "Malformed upload")
		}
		if p.FormName() == "metadata" {
			b, _ := io.ReadAll(io.LimitReader(p, 65537))
			if len(b) > 65536 || json.Unmarshal(b, &meta) != nil {
				return nil, 0, fail(400, "METADATA_INVALID", "Metadata must be a bounded JSON object")
			}
		} else if p.FormName() == "files" {
			if len(files) >= 10 {
				return nil, 0, fail(413, "TOO_MANY_FILES", "Maximum 10 files")
			}
			b, err := io.ReadAll(io.LimitReader(p, q.s.cfg.MaxFileBytes+1))
			if err != nil || int64(len(b)) > q.s.cfg.MaxFileBytes {
				return nil, 0, fail(413, "FILE_TOO_LARGE", "File exceeds configured limit")
			}
			total += int64(len(b))
			if total > q.s.cfg.MaxBatchBytes {
				return nil, 0, fail(413, "BATCH_TOO_LARGE", "Batch exceeds configured limit")
			}
			files = append(files, file{filepath.Base(p.FileName()), b})
		}
	}
	if len(files) == 0 {
		return nil, 0, fail(422, "FILES_REQUIRED", "Select at least one file")
	}
	if batchID := str(meta["clientBatchId"]); batchID != "" {
		for _, batch := range q.list("imports") {
			if batch["actor"] == q.actor.Actor && batch["clientBatchId"] == batchID {
				return nil, 0, fail(409, "BATCH_EXISTS", "This clientBatchId already has a durable import result")
			}
		}
	}
	if str(meta["origin"]) == "demo" && !q.isDemo() {
		return nil, 0, fail(403, "DEMO_PROJECT_REQUIRED", "Demo assets belong only in demo projects")
	}
	var linkedIssue, beforeLink Record
	if iid := str(meta["issueId"]); iid != "" {
		var e *apiError
		linkedIssue, e = q.get("issues", iid)
		if e != nil {
			return nil, 0, e
		}
		if e = confirmAssetContext(linkedIssue, meta, meta); e != nil {
			return nil, 0, e
		}
		if len(assetContextDifferences(linkedIssue, meta)) > 0 && fmt.Sprint(meta["issueRevision"]) != fmt.Sprint(linkedIssue["revision"]) {
			return nil, 0, fail(409, "REVISION_CONFLICT", "Issue changed; review the batch identity differences again")
		}
		beforeLink = clone(linkedIssue)
	}
	assets := []Record{}
	for _, f := range files {
		a, e := q.addAsset(f.name, f.raw, meta)
		if e != nil {
			return nil, 0, e
		}
		assets = append(assets, a)
	}
	if linkedIssue != nil {
		linkedIssue["updatedAt"] = now()
		reason := strings.TrimSpace(str(meta["reason"]))
		if reason == "" {
			reason = "导入日志并关联问题"
		}
		q.history(linkedIssue, beforeLink, "edited", reason)
	}
	batch := q.s.state.add("imports", q.project, Record{"assets": assets, "status": "completed", "actor": q.actor.Actor, "clientBatchId": str(meta["clientBatchId"]), "revision": 1})
	return batch, 201, nil
}
func (q *request) addAsset(name string, b []byte, meta Record) (Record, *apiError) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fail(422, "EMPTY_FILE", "Empty files cannot be imported")
	}
	declared := str(meta["declaredFormat"])
	if detected := detectProtocol(b); detected != "" {
		declared = detected
	}
	if declared == "native" && !q.s.cfg.Protocols.NativeReady() {
		return nil, fail(503, "CONNECTOR_UNCONFIGURED", "Native decoder key/IV are not configured")
	}
	if declared == "web" && !q.s.cfg.Protocols.WebReady() {
		return nil, fail(503, "CONNECTOR_UNCONFIGURED", "Web protocol decoder is not configured")
	}
	if declared != "native" && declared != "web" && (!utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || bytes.HasPrefix(b, []byte("PK\x03\x04")) || bytes.HasPrefix(b, []byte{0x1f, 0x8b})) {
		return nil, fail(422, "PROTOCOL_UNCONFIGURED", "Only UTF-8 JSON, JSONL and text are supported; native/encrypted/compressed protocol adapters are unconfigured")
	}
	stored := 0
	for _, raw := range q.s.state.bucket("raw") {
		stored += num(raw["size"])
	}
	if stored+len(b) > 256<<20 {
		return nil, fail(507, "STORE_CAPACITY", "Local store capacity is 256 MiB; remove expired content or use a larger deployment")
	}
	origin := str(meta["origin"])
	if origin == "" {
		origin = "import"
	}
	a := q.s.state.add("assets", q.project, Record{"name": name, "origin": origin, "declaredFormat": declared, "identitySource": "operator_declared", "platform": str(meta["platform"]), "device": str(meta["device"]), "version": str(meta["version"]), "build": str(meta["build"]), "app": str(meta["app"]), "environment": str(meta["environment"]), "rawHash": digest(b), "size": len(b), "expiresAt": time.Now().UTC().Add(q.s.cfg.Retention).Format(time.RFC3339Nano), "issueId": str(meta["issueId"]), "revision": 1})
	q.s.state.bucket("raw")[str(a["id"])] = Record{"projectId": q.project, "content": base64.StdEncoding.EncodeToString(b), "size": len(b)}
	if iid := str(meta["issueId"]); iid != "" {
		i, _ := q.get("issues", iid)
		i["assetIds"] = unique(append(stringsOf(i["assetIds"]), str(a["id"])))
		i["revision"] = num(i["revision"]) + 1
	}
	return a, nil
}
func (q *request) raw(a Record) ([]byte, *apiError) {
	if q.expired(a) {
		return nil, fail(410, "SOURCE_EXPIRED", "Original asset expired; upload a new file")
	}
	raw := q.s.state.bucket("raw")[str(a["id"])]
	if raw == nil {
		return nil, fail(410, "SOURCE_EXPIRED", "Original asset is unavailable")
	}
	b, err := base64.StdEncoding.DecodeString(str(raw["content"]))
	if err != nil {
		return nil, fail(503, "STORE_CORRUPT", "Original asset could not be read")
	}
	return b, nil
}
func (q *request) startParse(a, b Record) (any, int, *apiError) {
	raw, err := q.raw(a)
	if err != nil {
		return nil, 0, err
	}
	format, mode := str(b["format"]), str(b["mode"])
	if format == "" {
		format = "auto"
	}
	if declared := str(a["declaredFormat"]); declared == "native" || declared == "web" {
		format = declared
	}
	if format == "native" && !q.s.cfg.Protocols.NativeReady() {
		return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Native protocol decoder is not configured")
	}
	if format == "web" && !q.s.cfg.Protocols.WebReady() {
		return nil, 0, fail(503, "CONNECTOR_UNCONFIGURED", "Web protocol decoder is not configured")
	}
	if mode == "" {
		mode = "strict"
	}
	if !contains([]string{"auto", "jsonl", "json", "text", "native", "web"}, format) || !contains([]string{"strict", "skip_invalid_records"}, mode) {
		return nil, 0, fail(422, "PARSER_INVALID", "Choose supported format and strict or skip_invalid_records mode")
	}
	active := 0
	for _, r := range q.s.state.bucket("parseRuns") {
		if r["status"] == "queued" || r["status"] == "running" {
			active++
		}
	}
	if active >= 2 {
		return nil, 0, fail(429, "PARSE_CAPACITY", "Two parse jobs are active; retry after one finishes")
	}
	run := q.s.state.add("parseRuns", q.project, Record{"assetId": a["id"], "status": "queued", "format": format, "mode": mode, "parserVersion": "workbench-1", "eventCount": 0, "errorCount": 0, "errors": []Record{}, "expiresAt": a["expiresAt"], "revision": 1})
	rid := str(run["id"])
	q.afterCommit = append(q.afterCommit, func() { q.s.launchParse(rid, raw) })
	return run, 201, nil
}
func (s *Server) persistJob(fn func()) bool {
	s.epoch++
	before := clone(s.state)
	fn()
	bytes, _ := json.Marshal(s.state)
	if int64(len(bytes)) > s.cfg.MaxStateBytes {
		s.state = before
		for _, kind := range []string{"parseRuns", "turns"} {
			for _, r := range s.state.bucket(kind) {
				if r["status"] == "running" || r["status"] == "queued" {
					r["status"] = "failed"
					r["error"] = Record{"code": "STORE_CAPACITY", "message": "Cumulative state capacity reached; no partial output was published"}
					if c := s.cancels[str(r["id"])]; c != nil {
						c()
					}
				}
			}
		}
		if err := s.store.Save(s.state); err != nil {
			s.unhealthy = err
		}
		return false
	}
	if err := s.store.Save(s.state); err != nil {
		s.state = before
		s.unhealthy = err
		return false
	}
	return true
}
func (s *Server) launchParse(rid string, raw []byte) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	s.cancels[rid] = cancel
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		defer cancel()
		s.mu.Lock()
		r := s.state.bucket("parseRuns")[rid]
		if r["status"] != "queued" || s.closed {
			delete(s.cancels, rid)
			s.mu.Unlock()
			return
		}
		format, mode, project, expiry := str(r["format"]), str(r["mode"]), str(r["projectId"]), r["expiresAt"]
		if !s.persistJob(func() { r["status"] = "running"; r["startedAt"] = now() }) {
			delete(s.cancels, rid)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		var events, diagnostics []Record
		fatal := ""
		if format == "native" || format == "web" {
			decoded, err := s.cfg.Protocols.Decode(ctx, format, raw)
			if err != nil {
				events = []Record{}
				diagnostics = []Record{}
				fatal = "PROTOCOL_DECODE_FAILED"
			} else {
				rows := []Record{}
				for _, v := range decoded {
					timestamp := ""
					if v.LogTime > 0 {
						t := time.Unix(v.LogTime, 0)
						if v.LogTime > 100000000000 {
							t = time.UnixMilli(v.LogTime)
						}
						timestamp = t.UTC().Format(time.RFC3339Nano)
					}
					rows = append(rows, Record{"content": v.Content, "logType": v.LogType, "type": v.LogType, "time": timestamp, "envelopeTime": v.LogTime, "originalLine": v.Line})
				}
				b, _ := json.Marshal(rows)
				events, diagnostics, fatal = parseRecords(ctx, b, "json", rid, project, s.cfg.MaxEvents)
			}
		} else {
			events, diagnostics, fatal = parseRecords(ctx, raw, format, rid, project, s.cfg.MaxEvents)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.cancels, rid)
		r = s.state.bucket("parseRuns")[rid]
		if r["status"] != "running" {
			return
		}
		s.persistJob(func() {
			status := "succeeded"
			if ctx.Err() != nil {
				status = "canceled"
				r["error"] = Record{"code": "PARSE_CANCELED", "message": "Parse canceled or timed out"}
			} else if fatal != "" || (len(diagnostics) > 0 && mode == "strict") || len(events) == 0 {
				status = "failed"
				if fatal == "" && len(events) == 0 {
					fatal = "EMPTY_RECORDS"
				}
				r["error"] = Record{"code": "PARSE_FAILED", "message": fatal}
			} else if len(diagnostics) > 0 {
				status = "partial"
			}
			if status == "succeeded" || status == "partial" {
				for _, ev := range events {
					ev["expiresAt"] = expiry
					s.state.bucket("events")[str(ev["id"])] = ev
				}
				r["eventCount"] = len(events)
			}
			r["status"] = status
			r["errorCount"] = len(diagnostics)
			if len(diagnostics) > 100 {
				diagnostics = diagnostics[:100]
				r["errorsTruncated"] = true
			}
			r["errors"] = diagnostics
			r["finishedAt"] = now()
			r["revision"] = num(r["revision"]) + 1
			s.state.add("notifications", project, Record{"kind": "parse", "targetId": rid, "message": "Parsing " + status, "read": false, "revision": 1})
		})
	}()
}
func parseRecords(ctx context.Context, raw []byte, format, rid, project string, max int) ([]Record, []Record, string) {
	events := []Record{}
	bad := []Record{}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	trim := bytes.TrimSpace(raw)
	if format == "auto" {
		if len(trim) > 0 && trim[0] == '[' {
			format = "json"
		} else if len(trim) > 0 && trim[0] == '{' {
			format = "jsonl"
		} else {
			format = "text"
		}
	}
	add := func(line int, value any) {
		if len(events)+len(bad) >= max {
			return
		}
		m := record(value)
		if len(m) == 0 {
			bad = append(bad, Record{"line": line, "message": "Record must be a nonempty object"})
			return
		}
		content := str(m["content"])
		if content == "" {
			content = str(m["message"])
		}
		if content == "" {
			b, _ := json.Marshal(m)
			content = string(b)
		}
		timestamp := str(m["time"])
		if timestamp == "" {
			timestamp = str(m["timestamp"])
		}
		normalized := ""
		if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
			normalized = parsed.UTC().Format(time.RFC3339Nano)
		}
		ev := Record{"id": rid + "-" + strconv.Itoa(line), "projectId": project, "runId": rid, "line": line, "time": normalized, "rawTime": timestamp, "level": strings.ToLower(str(m["level"])), "module": str(m["module"]), "content": content, "fields": m, "requestId": str(m["requestId"]), "file": str(m["file"]), "sourceLine": num(m["sourceLine"]), "type": m["type"], "logType": m["logType"]}
		normalizePageDiagnostic(ev)
		events = append(events, ev)
	}
	if format == "json" {
		var values []any
		if json.Unmarshal(raw, &values) != nil {
			return events, bad, "INVALID_JSON_ARRAY"
		}
		if len(values) > max {
			return events, bad, "EVENT_LIMIT_EXCEEDED"
		}
		for i, v := range values {
			if ctx.Err() != nil {
				return nil, bad, "CANCELED"
			}
			add(i+1, v)
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		line := 0
		for scanner.Scan() {
			line++
			if ctx.Err() != nil {
				return nil, bad, "CANCELED"
			}
			if line > max {
				return nil, bad, "EVENT_LIMIT_EXCEEDED"
			}
			txt := scanner.Text()
			if strings.TrimSpace(txt) == "" {
				continue
			}
			if format == "text" {
				add(line, Record{"content": txt})
			} else {
				var m map[string]any
				if err := json.Unmarshal([]byte(txt), &m); err != nil || m == nil {
					bad = append(bad, Record{"line": line, "message": "Invalid JSON object"})
				} else {
					add(line, m)
				}
			}
		}
		if scanner.Err() != nil {
			return nil, bad, "LINE_LIMIT_EXCEEDED"
		}
	}
	return events, bad, ""
}
func (q *request) filtered(run Record, values url.Values) ([]Record, string, *apiError) {
	if q.expired(run) {
		return nil, "", fail(410, "SOURCE_EXPIRED", "Parsed events expired")
	}
	normalized := url.Values{}
	for _, key := range []string{"query", "level", "module", "type", "unknownTime", "start", "end"} {
		if v := values.Get(key); v != "" {
			normalized.Set(key, v)
		}
	}
	filterHash := digest([]byte(str(run["id"]) + "?" + normalized.Encode()))
	var start, end time.Time
	var err error
	if v := values.Get("start"); v != "" {
		start, err = time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, "", fail(422, "TIME_INVALID", "Start must be ISO timestamp")
		}
	}
	if v := values.Get("end"); v != "" {
		end, err = time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, "", fail(422, "TIME_INVALID", "End must be ISO timestamp")
		}
	}
	out := []Record{}
	for _, ev := range q.s.state.bucket("events") {
		if ev["projectId"] != q.project || ev["runId"] != run["id"] {
			continue
		}
		if v := values.Get("query"); v != "" && !strings.Contains(strings.ToLower(str(ev["content"])), strings.ToLower(v)) {
			continue
		}
		if v := values.Get("level"); v != "" && !strings.EqualFold(v, str(ev["level"])) {
			continue
		}
		if v := values.Get("module"); v != "" && v != str(ev["module"]) {
			continue
		}
		if v := values.Get("type"); v != "" && v != fmt.Sprint(ev["type"]) && v != fmt.Sprint(ev["logType"]) {
			continue
		}
		if values.Get("unknownTime") == "only" && str(ev["time"]) != "" {
			continue
		}
		if values.Get("unknownTime") == "exclude" && str(ev["time"]) == "" {
			continue
		}
		t, te := time.Parse(time.RFC3339Nano, str(ev["time"]))
		if (!start.IsZero() || !end.IsZero()) && (te != nil || (!start.IsZero() && t.Before(start)) || (!end.IsZero() && t.After(end))) {
			continue
		}
		out = append(out, clone(ev))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := str(out[i]["time"]), str(out[j]["time"])
		if a == b {
			if left, right := num(out[i]["line"]), num(out[j]["line"]); left != right {
				return left < right
			}
			return str(out[i]["id"]) < str(out[j]["id"])
		}
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		ta, ea := time.Parse(time.RFC3339Nano, a)
		tb, eb := time.Parse(time.RFC3339Nano, b)
		if ea == nil && eb == nil {
			return ta.Before(tb)
		}
		return a < b
	})
	return out, filterHash, nil
}
func (q *request) readEvents(run Record, mode string) (any, int, *apiError) {
	events, hash, e := q.filtered(run, q.r.URL.Query())
	if e != nil {
		return nil, 0, e
	}
	if mode == "histogram" {
		if q.r.URL.Query().Get("bins") == "24" {
			return q.adaptiveHistogram(run, events, hash), 200, nil
		}
		counts := map[string]int{}
		unknown := 0
		for _, ev := range events {
			if str(ev["time"]) == "" {
				unknown++
				continue
			}
			t, _ := time.Parse(time.RFC3339Nano, str(ev["time"]))
			counts[t.Truncate(time.Minute).Format(time.RFC3339)]++
		}
		keys := []string{}
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buckets := []Record{}
		for _, k := range keys {
			buckets = append(buckets, Record{"time": k, "count": counts[k]})
		}
		return Record{"buckets": buckets, "unknownTime": unknown, "total": len(events), "filterHash": hash, "runId": run["id"]}, 200, nil
	}
	offset := 0
	if cursor := q.r.URL.Query().Get("cursor"); cursor != "" {
		pieces := strings.Split(cursor, ":")
		if len(pieces) != 2 || pieces[0] != hash {
			return nil, 0, fail(422, "CURSOR_INVALID", "Cursor belongs to a different filter")
		}
		var err error
		offset, err = strconv.Atoi(pieces[1])
		if err != nil {
			return nil, 0, fail(422, "CURSOR_INVALID", "Cursor offset is invalid")
		}
		if offset < 0 || offset > len(events) {
			return nil, 0, fail(422, "CURSOR_INVALID", "Cursor is out of range")
		}
	}
	limit, _ := strconv.Atoi(q.r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	end := offset + limit
	if end > len(events) {
		end = len(events)
	}
	next := ""
	if end < len(events) {
		next = hash + ":" + strconv.Itoa(end)
	}
	return Record{"events": events[offset:end], "total": len(events), "nextCursor": next, "filterHash": hash, "runId": run["id"], "truncated": end < len(events)}, 200, nil
}
func (q *request) contextEvents(event Record) (any, int, *apiError) {
	run, err := q.get("parseRuns", str(event["runId"]))
	if err != nil {
		return nil, 0, err
	}
	events, _, e := q.filtered(run, url.Values{})
	if e != nil {
		return nil, 0, e
	}
	result := []Record{}
	for _, ev := range events {
		if n := num(ev["line"]) - num(event["line"]); n >= -5 && n <= 5 {
			result = append(result, ev)
		}
	}
	return Record{"events": result, "eventId": event["id"], "radius": 5}, 200, nil
}
func (q *request) related(event Record) (any, int, *apiError) {
	if q.expired(event) {
		return nil, 0, fail(410, "SOURCE_EXPIRED", "Event expired")
	}
	result := []Record{}
	requestID := str(event["requestId"])
	if requestID != "" {
		for _, ev := range q.s.state.bucket("events") {
			if ev["projectId"] == q.project && ev["runId"] == event["runId"] && ev["requestId"] == requestID && ev["id"] != event["id"] {
				result = append(result, clone(ev))
				if len(result) >= 100 {
					break
				}
			}
		}
	}
	return Record{"events": result, "identityGaps": []string{"Request ID is a candidate correlation; application, environment, device/session, build and time boundaries require verification"}, "candidateOnly": true, "truncated": len(result) >= 100}, 200, nil
}
func (q *request) export(b Record) (any, int, *apiError) {
	if e := q.permission("export"); e != nil {
		return nil, 0, e
	}
	if str(b["kind"]) == "raw" {
		if e := q.permission("raw"); e != nil {
			return nil, 0, e
		}
		asset, e := q.get("assets", str(b["assetId"]))
		if e != nil {
			return nil, 0, e
		}
		if _, e = q.raw(asset); e != nil {
			return nil, 0, e
		}
		exp := q.s.state.add("exports", q.project, Record{"kind": "raw", "assetId": asset["id"], "expiresAt": asset["expiresAt"], "filename": asset["name"]})
		return Record{"id": exp["id"], "expiresAt": exp["expiresAt"]}, 201, nil
	}
	var out any
	expiry := time.Now().UTC().Add(q.s.cfg.Retention).Format(time.RFC3339Nano)
	if str(b["kind"]) == "events" {
		run, e := q.get("parseRuns", str(b["runId"]))
		if e != nil {
			return nil, 0, e
		}
		v := url.Values{}
		for _, k := range []string{"query", "level", "module", "type", "unknownTime", "start", "end"} {
			v.Set(k, str(b[k]))
		}
		events, hash, e := q.filtered(run, v)
		if e != nil {
			return nil, 0, e
		}
		out = Record{"events": events, "filterHash": hash, "runId": run["id"], "total": len(events), "truncated": false}
		expiry = str(run["expiresAt"])
	} else if str(b["kind"]) == "investigation" || str(b["kind"]) == "issue" {
		issue, e := q.get("issues", str(b["issueId"]))
		if e != nil {
			return nil, 0, e
		}
		out = Record{"issue": issue, "evidence": q.forIssue("evidence", str(issue["id"])), "turns": q.forIssue("turns", str(issue["id"])), "verifications": q.forIssue("verifications", str(issue["id"])), "updates": q.forIssue("updates", str(issue["id"]))}
	} else {
		return nil, 0, fail(422, "EXPORT_KIND", "Choose raw, events or investigation")
	}
	_ = q.redact(out)
	exp := q.s.state.add("exports", q.project, Record{"payload": out, "expiresAt": expiry, "kind": b["kind"]})
	return Record{"id": exp["id"], "expiresAt": expiry}, 201, nil
}

func detectProtocol(b []byte) string {
	if len(b) > 0 && b[0] == 1 {
		return "native"
	}
	trim := bytes.TrimSpace(b)
	var array []map[string]any
	if json.Unmarshal(trim, &array) == nil {
		for _, row := range array {
			if _, ok := row["logArray"]; ok {
				return "web"
			}
			if row["v"] != nil && row["l"] != nil {
				return "web"
			}
		}
	}
	var m map[string]any
	if json.Unmarshal(trim, &m) == nil {
		if _, present := m["logArray"]; present {
			return "web"
		}
		if m["v"] != nil && m["l"] != nil {
			return "web"
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(trim))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		segment := strings.SplitN(string(line), ",", 2)[0]
		if decoded, err := url.QueryUnescape(segment); err == nil {
			var row map[string]any
			if json.Unmarshal([]byte(decoded), &row) == nil && row["v"] != nil && row["l"] != nil {
				return "web"
			}
		}
		var candidate map[string]any
		if json.Unmarshal(line, &candidate) == nil {
			if _, ok := candidate["logArray"]; ok {
				return "web"
			}
			if candidate["v"] != nil && candidate["l"] != nil {
				return "web"
			}
		}
	}
	first := strings.SplitN(string(trim), ",", 2)[0]
	decoded, err := url.QueryUnescape(first)
	if err == nil && json.Unmarshal([]byte(decoded), &m) == nil && m["v"] != nil && m["l"] != nil {
		return "web"
	}
	return ""
}
