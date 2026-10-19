package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func validateExtraFixture(f fixture, large bool) error {
	if large {
		// Reserve the maximum synthetic identity length before any HTTP writes.
		web := make(map[string]any, len(f.WebBody)+1)
		for key, value := range f.WebBody {
			web[key] = value
		}
		web["deviceId"] = strings.Repeat("x", 128)
		body, err := json.Marshal(web)
		if err != nil || len(body) > 32<<20 {
			return errors.New("web wire bounds")
		}
	}
	limit := 2 << 20
	if large {
		limit = 128 << 20
	}
	for _, v := range []string{f.NativePlainBase64, f.NativeDownloadBase64} {
		if v != "" {
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil || len(b) > limit {
				return errors.New("download oracle bounds")
			}
		}
	}
	total := 0
	for _, v := range f.NativeContents {
		total += len(v)
	}
	if total > 128<<20 {
		return errors.New("native plaintext bounds")
	}
	total = 0
	for _, v := range f.WebContents {
		total += len(v)
	}
	if total > 128<<20 {
		return errors.New("web plaintext bounds")
	}
	rawBytes := 0
	for _, raw := range f.WebRawLogs {
		if len(raw) > 4<<20 {
			return errors.New("web raw record bounds")
		}
		rawBytes += len(raw)
	}
	if rawBytes > 32<<20 {
		return errors.New("web raw byte bounds")
	}
	if len(f.WebRawLogs) > 16384 {
		return errors.New("web raw bounds")
	}
	return nil
}

func ownedOSS(base, target string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Port() != "8080" {
		return false
	}
	match := regexp.MustCompile(`^(logan-verifier-[a-f0-9]{32})-go$`).FindStringSubmatch(b.Hostname())
	if len(match) != 2 {
		return false
	}
	host := match[1] + "-g-o"
	if u.Hostname() != host && u.Hostname() != "fixture."+host {
		return false
	}
	for key, values := range u.Query() {
		if (strings.EqualFold(key, "signature") || strings.EqualFold(key, "x-oss-signature")) && len(values) == 1 && values[0] != "" {
			return true
		}
	}
	return false
}

func latestContract(d json.RawMessage) string {
	var rows []struct {
		TaskID   int64  `json:"taskId"`
		DeviceID string `json:"deviceId"`
		LogDate  int64  `json:"logDate"`
		AddTime  int64  `json:"addTime"`
	}
	if json.Unmarshal(d, &rows) != nil || len(rows) == 0 || len(rows) > 20 {
		return "latest_contract"
	}
	for _, r := range rows {
		if r.TaskID <= 0 || !strings.HasPrefix(r.DeviceID, "phase19-") || r.LogDate <= 0 || r.AddTime <= 0 {
			return "latest_contract"
		}
	}
	return ""
}

func nativePlain(b []byte) ([]byte, error) {
	r := bytes.NewReader(b)
	out := []byte{}
	for r.Len() > 0 {
		z, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		z.Multistream(false)
		part, err := io.ReadAll(io.LimitReader(z, int64((128<<20)-len(out))+1))
		z.Close()
		if err != nil || len(out)+len(part) > 128<<20 {
			return nil, errors.New("gzip bounds")
		}
		out = append(out, part...)
		if r.Len() == 0 {
			break
		}
		next, _ := r.ReadByte()
		_ = r.UnreadByte()
		if next == 0x1f {
			continue
		}
		pad, _ := r.ReadByte()
		count := int(pad)
		if pad == 0 {
			count = 1
			for r.Len() > 0 {
				v, _ := r.ReadByte()
				if v != 0 {
					_ = r.UnreadByte()
					break
				}
				count++
			}
			if count > 16 {
				return nil, errors.New("padding")
			}
		} else {
			if count > 16 {
				return nil, errors.New("padding")
			}
			for i := 1; i < count; i++ {
				v, er := r.ReadByte()
				if er != nil || v != pad {
					return nil, errors.New("padding")
				}
			}
		}
	}
	return out, nil
}

// rawCall never follows redirects and records only bounded classifications.
func (e *engine) rawCall(ctx context.Context, route, target string, check func(*http.Response, []byte) string) string {
	e.budgetMu.Lock()
	if e.requests >= 1000000 {
		e.budgetMu.Unlock()
		return "request_limit"
	}
	e.requests++
	e.budgetMu.Unlock()
	one, cancel := context.WithTimeout(ctx, e.c.Timeout)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(one, "GET", target, nil)
	kind := "transport"
	if err == nil {
		client := *e.client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, er := client.Do(req)
		if er == nil {
			b, er := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
			resp.Body.Close()
			switch {
			case er != nil:
				kind = "body_read"
			case len(b) > 64<<20:
				kind = "body_limit"
			default:
				kind = check(resp, b)
			}
		}
	}
	if one.Err() != nil {
		kind = "timeout"
		if errors.Is(one.Err(), context.Canceled) {
			kind = "cancelled"
		}
	}
	e.stats.record(route, float64(time.Since(start))/float64(time.Millisecond), kind, false)
	return kind
}

func (e *engine) all20Flow(ctx context.Context, index int, flow string) (l ledger) {
	l = ledger{Cycle: index, Flow: flow}
	start := time.Now()
	defer func() {
		e.stats.record("workflow", float64(time.Since(start))/float64(time.Millisecond), l.Classification, true)
	}()
	device := fmt.Sprintf("phase19-%s-%d", e.runID, index)
	native := flow == "native"
	prefix := "/logan/web"
	upload := prefix + "/upload.json"
	expected := e.f.WebContents
	body := []byte{}
	headers := map[string]string{"Content-Type": "application/json"}
	if native {
		prefix = "/logan/task"
		upload = "/logan/upload.json"
		expected = e.f.NativeContents
		body, _ = base64.StdEncoding.DecodeString(e.f.NativeBase64)
		headers = map[string]string{"Content-Type": "application/octet-stream", "appId": "logan-load-test", "deviceId": device, "unionId": "phase19-synthetic", "platform": "1", "fileDate": "2026-10-15", "buildVersion": "1", "appVersion": "1"}
	} else {
		web := map[string]any{}
		for k, v := range e.f.WebBody {
			web[k] = v
		}
		web["deviceId"] = device
		body, _ = json.Marshal(web)
	}
	call := func(label, path string, check func(json.RawMessage) string) bool {
		_, l.Classification = e.call(ctx, flow+"-"+label, "GET", path, nil, nil, check)
		return l.Classification == ""
	}
	uploadURL := ""
	uploadStart := e.stats.beginE2E(flow)
	var queryableElapsed time.Duration
	defer func() { e.stats.recordE2E(flow, l, uploadStart, queryableElapsed) }()
	_, l.Classification = e.call(ctx, flow+"-upload", "POST", upload, body, headers, func(d json.RawMessage) string {
		if !native {
			var accepted bool
			if json.Unmarshal(d, &accepted) != nil || !accepted {
				return "upload_contract"
			}
		}
		if native && (json.Unmarshal(d, &uploadURL) != nil || !strings.HasPrefix(uploadURL, "/logan/downing?name=")) {
			return "upload_contract"
		}
		return ""
	})
	if l.Classification != "" {
		return
	}
	search := prefix + "/search.json?deviceId=" + device + "&beginTime=1&endTime=4102444800000"
	if native {
		search += "&platform=1"
	}
	if !call("search", search, func(d json.RawMessage) string {
		var rows []struct {
			TaskID   int64  `json:"taskId"`
			DeviceID string `json:"deviceId"`
		}
		if json.Unmarshal(d, &rows) != nil || len(rows) != 1 || rows[0].TaskID <= 0 || rows[0].DeviceID != device {
			return "task_discovery"
		}
		l.TaskID = rows[0].TaskID
		return ""
	}) {
		return
	}
	task := strconv.FormatInt(l.TaskID, 10)
	latest := prefix + "/latest.json"
	if native {
		latest = "/logan/latest.json"
	}
	if !call("latest", latest, func(d json.RawMessage) string {
		if kind := latestContract(d); kind != "" {
			return kind
		}
		var rows []struct {
			TaskID   int64  `json:"taskId"`
			DeviceID string `json:"deviceId"`
		}
		if json.Unmarshal(d, &rows) != nil {
			return "latest_contract"
		}
		for _, row := range rows {
			if row.TaskID == l.TaskID && row.DeviceID == device {
				return ""
			}
		}
		return "latest_identity"
	}) {
		return
	}
	if native && !call("types", "/logan/meta/logtypes.json", func(d json.RawMessage) string {
		var v []struct {
			First  int    `json:"first"`
			Second string `json:"second"`
		}
		if json.Unmarshal(d, &v) != nil || len(v) != 3 {
			return "types_contract"
		}
		for i, r := range v {
			if r.First != i+1 || r.Second != fmt.Sprintf("日志类型%d", i+1) {
				return "types_contract"
			}
		}
		return ""
	}) {
		return
	}
	path := prefix + "/detailIndex.json?tasks=" + task + "&logTypes=&keyword=&beginTime=00:00&endTime=23:59"
	label := "index"
	if native {
		path = prefix + "/" + task + "/brief.json?logTypes=&keyword="
		label = "brief"
	}
	parseCtx, cancel := context.WithTimeout(ctx, e.c.ParseTimeout)
	defer cancel()
	for {
		_, l.Classification = e.call(parseCtx, flow+"-"+label, "GET", path, nil, nil, func(d json.RawMessage) string {
			l.DetailIDs = ids(d)
			if len(l.DetailIDs) > len(expected) {
				return "index_contract"
			}
			seen := map[int64]bool{}
			for _, id := range l.DetailIDs {
				if seen[id] {
					return "index_contract"
				}
				seen[id] = true
			}
			return ""
		})
		if l.Classification != "" {
			return
		}
		if len(l.DetailIDs) == len(expected) {
			break
		}
		select {
		case <-parseCtx.Done():
			l.Classification = "parse_timeout"
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	metaLabel := "task"
	metaPath := prefix + "/taskDetail.json?tasks=" + task
	if native {
		metaLabel = "info"
		metaPath = prefix + "/" + task + "/info.json"
	}
	if !call(metaLabel, metaPath, func(d json.RawMessage) string {
		var r struct {
			TaskID   int64  `json:"taskId"`
			DeviceID string `json:"deviceId"`
		}
		if json.Unmarshal(d, &r) != nil || r.TaskID != l.TaskID || r.DeviceID != device {
			return "task_contract"
		}
		return ""
	}) {
		return
	}
	actual := []string{}
	firstContent := ""
	for offset := 0; offset < len(l.DetailIDs); offset += 200 {
		end := offset + 200
		if end > len(l.DetailIDs) {
			end = len(l.DetailIDs)
		}
		requested := l.DetailIDs[offset:end]
		parts := []string{}
		for _, id := range requested {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		detailPath := prefix + "/details.json?detailIds=" + strings.Join(parts, ",")
		if native {
			detailPath = prefix + "/query/details.json?detailIds=" + strings.Join(parts, ",")
		}
		if !call("details", detailPath, func(d json.RawMessage) string {
			var rows []struct {
				ID      int64  `json:"id"`
				Content string `json:"content"`
			}
			if json.Unmarshal(d, &rows) != nil || len(rows) != len(requested) {
				return "detail_identity"
			}
			want := map[int64]bool{}
			for _, id := range requested {
				want[id] = true
			}
			for _, r := range rows {
				if !want[r.ID] {
					return "detail_identity"
				}
				delete(want, r.ID)
				actual = append(actual, r.Content)
				if r.ID == l.DetailIDs[0] {
					firstContent = r.Content
				}
			}
			if len(want) != 0 {
				return "detail_identity"
			}
			if offset+len(requested) == len(l.DetailIDs) {
				return classify(actual, expected, flow)
			}
			return ""
		}) {
			return
		}
	}
	queryableElapsed = time.Since(uploadStart)
	single := prefix + "/logDetail.json?detailId=" + strconv.FormatInt(l.DetailIDs[0], 10)
	if native {
		single = prefix + "/" + strconv.FormatInt(l.DetailIDs[0], 10) + "/detail.json"
	}
	if !call("detail", single, func(d json.RawMessage) string {
		var r struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
		}
		if json.Unmarshal(d, &r) != nil || r.ID != l.DetailIDs[0] || r.Content != firstContent {
			return "detail_contract"
		}
		return ""
	}) {
		return
	}
	if !native && !call("exception", prefix+"/exception.json", func(d json.RawMessage) string {
		var s string
		if json.Unmarshal(d, &s) != nil || s != "exception" {
			return "exception_contract"
		}
		return ""
	}) {
		return
	}
	download := prefix + "/getDownLoadUrl.json?tasks=" + task
	wantURL := prefix + "/download.json?tasks=" + task
	if native {
		download = prefix + "/" + task + "/download.json"
		wantURL = uploadURL
	}
	downloadURL := ""
	if !call("download-url", download, func(d json.RawMessage) string {
		if json.Unmarshal(d, &downloadURL) != nil || downloadURL != wantURL {
			return "download_contract"
		}
		return ""
	}) {
		return
	}
	if native {
		location := ""
		l.Classification = e.rawCall(ctx, "native-download", e.c.Base+downloadURL, func(r *http.Response, b []byte) string {
			location = r.Header.Get("Location")
			if r.StatusCode != 302 || !ownedOSS(e.c.Base, location) {
				return "redirect_contract"
			}
			return ""
		})
		if l.Classification != "" {
			return
		}
		l.Classification = e.rawCall(ctx, "native-download", location, func(r *http.Response, b []byte) string {
			want, _ := base64.StdEncoding.DecodeString(e.f.NativeDownloadBase64)
			plainWant, _ := base64.StdEncoding.DecodeString(e.f.NativePlainBase64)
			if r.StatusCode != 200 || !bytes.Equal(b, want) {
				return "download_content"
			}
			plain, err := nativePlain(b)
			if err != nil || !bytes.Equal(plain, plainWant) {
				return "download_plaintext"
			}
			return ""
		})
	} else {
		l.Classification = e.rawCall(ctx, "web-download", e.c.Base+downloadURL, func(r *http.Response, b []byte) string {
			var logs []string
			if r.StatusCode != 201 || !strings.Contains(r.Header.Get("Content-Type"), "application/octet-stream") || json.Unmarshal(b, &logs) != nil || !reflect.DeepEqual(logs, e.f.WebRawLogs) {
				return "download_content"
			}
			return ""
		})
	}
	return
}
