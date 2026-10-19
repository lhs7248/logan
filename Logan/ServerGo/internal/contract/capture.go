package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Snapshots are local evidence, not golden fixtures invented from the Go implementation.
// Body is base64-encoded by encoding/json so binary downloads round-trip exactly.
type Snapshot struct {
	Version        int               `json:"version"`
	CaseID         string            `json:"case_id"`
	CapturedAt     time.Time         `json:"captured_at"`
	ManifestSHA256 string            `json:"manifest_sha256,omitempty"`
	Status         int               `json:"status"`
	Headers        map[string]string `json:"headers"`
	Body           []byte            `json:"body_base64"`
}

func Capture(ctx context.Context, base string, c Case, vars map[string]string, allowSideEffects bool) (Snapshot, error) {
	var out Snapshot
	if c.SideEffects && !allowSideEffects {
		return out, fmt.Errorf("case %s may write data or emit errors; explicit opt-in required", c.ID)
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return out, fmt.Errorf("base URL must be http(s) with optional context path and no credentials/query")
	}
	target, err := Expand(c.Path, vars)
	if err != nil {
		return out, err
	}
	relative, err := url.Parse(target)
	if err != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(relative.Path, "/") || strings.HasPrefix(relative.Path, "//") || path.Clean(relative.Path) != relative.Path || relative.Fragment != "" {
		return out, fmt.Errorf("invalid case request path")
	}
	body, err := Expand(c.Body, vars)
	if err != nil {
		return out, err
	}
	data := []byte(body)
	if c.BodyFile != "" {
		file, err := Expand(c.BodyFile, vars)
		if err != nil {
			return out, err
		}
		data, err = os.ReadFile(file)
		if err != nil {
			return out, fmt.Errorf("cannot read fixture for %s", c.ID)
		}
	}
	endpoint := strings.TrimRight(base, "/") + target
	req, err := http.NewRequestWithContext(ctx, c.Method, endpoint, bytes.NewReader(data))
	if err != nil {
		return out, fmt.Errorf("cannot create case request")
	}
	for key, v := range c.Headers {
		value, err := Expand(v, vars)
		if err != nil {
			return out, err
		}
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("case %s request failed (check connectivity)", c.ID)
	}
	defer resp.Body.Close()
	const maxCapture = 64 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxCapture+1))
	if err != nil {
		return out, fmt.Errorf("read response for %s failed", c.ID)
	}
	if len(b) > maxCapture {
		return out, fmt.Errorf("response exceeds 64 MiB capture budget")
	}
	headers := map[string]string{}
	for _, key := range []string{"Content-Type", "Content-Disposition", "Location", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Max-Age"} {
		if v := resp.Header.Get(key); v != "" {
			headers[key] = v
		}
	}
	return Snapshot{Version: 1, CaseID: c.ID, CapturedAt: time.Now().UTC(), Status: resp.StatusCode, Headers: headers, Body: b}, nil
}
func SaveSnapshot(file string, s Snapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(b, '\n'), 0600)
}
func LoadSnapshot(file string) (Snapshot, error) {
	var s Snapshot
	b, err := os.ReadFile(file)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	if err != nil {
		return s, err
	}
	if s.Version != 1 {
		return s, fmt.Errorf("unsupported snapshot version")
	}
	return s, nil
}
