package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const PushCallbackPath = "/logan-web/api/workbench/v1/integrations/push/events"

type PushConfig struct {
	Endpoint        string   `json:"endpoint"`
	Token           string   `json:"token"`
	CallbackURL     string   `json:"callbackURL"`
	CallbackSecret  string   `json:"callbackSecret"`
	Projects        []string `json:"projects"`
	DeadlineSeconds int      `json:"deadlineSeconds"`
}
type Push struct {
	config PushConfig
	client *http.Client
}

func pushURL(value string) bool {
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	return u.Scheme == "https" || (u.Scheme == "http" && (h == "localhost" || (ip != nil && ip.IsLoopback())))
}
func NewPush(c PushConfig) (*Push, error) {
	if c.Endpoint == "" {
		if c.Token != "" || c.CallbackURL != "" || c.CallbackSecret != "" || len(c.Projects) > 0 {
			return nil, ErrInvalidRequest
		}
		return &Push{}, nil
	}
	if !pushURL(c.Endpoint) || !pushURL(c.CallbackURL) || len(c.Token) < 32 || len(c.CallbackSecret) < 32 || c.Token == c.CallbackSecret {
		return nil, ErrInvalidRequest
	}
	u, _ := url.Parse(c.CallbackURL)
	if u.Path != PushCallbackPath {
		return nil, ErrInvalidRequest
	}
	if c.DeadlineSeconds == 0 {
		c.DeadlineSeconds = 600
	}
	if c.DeadlineSeconds < 60 || c.DeadlineSeconds > 86400 {
		return nil, ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, p := range c.Projects {
		if strings.TrimSpace(p) == "" || seen[p] {
			return nil, ErrInvalidRequest
		}
		seen[p] = true
	}
	if len(seen) == 0 {
		return nil, ErrInvalidRequest
	}
	return &Push{config: c, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect prohibited") }}}, nil
}
func LoadPushConfig(file string, projects []string) (*Push, error) {
	if file == "" {
		return NewPush(PushConfig{})
	}
	p, e := filepath.Abs(file)
	if e != nil {
		return nil, ErrInvalidRequest
	}
	for current := p; ; current = filepath.Dir(current) {
		i, e := os.Lstat(current)
		if e != nil || i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrInvalidRequest
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, ErrInvalidRequest
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 {
		return nil, ErrInvalidRequest
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return nil, ErrInvalidRequest
	}
	var c PushConfig
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalidRequest
	}
	for _, p := range c.Projects {
		found := false
		for _, known := range projects {
			if p == known {
				found = true
			}
		}
		if !found {
			return nil, ErrInvalidRequest
		}
	}
	return NewPush(c)
}
func (p *Push) Ready(project string) bool {
	if p == nil {
		return false
	}
	for _, v := range p.config.Projects {
		if v == project {
			return true
		}
	}
	return false
}
func (p *Push) Deadline() time.Duration { return time.Duration(p.config.DeadlineSeconds) * time.Second }
func (p *Push) CallbackURL() string     { return p.config.CallbackURL }
func (p *Push) CallbackSecret() string {
	if p == nil {
		return ""
	}
	return p.config.CallbackSecret
}

// Dispatch never treats a lost response as a known rejection.
func (p *Push) Dispatch(ctx context.Context, payload any) (status, provider string) {
	b, e := json.Marshal(payload)
	if e != nil {
		return "unknown", ""
	}
	var identity struct {
		AttemptID string `json:"attemptId"`
	}
	_ = json.Unmarshal(b, &identity)
	r, e := http.NewRequestWithContext(ctx, "POST", p.config.Endpoint, bytes.NewReader(b))
	if e != nil {
		return "unknown", ""
	}
	r.GetBody = nil // Never let the transport replay a potentially executed POST.
	r.Header.Set("Authorization", "Bearer "+p.config.Token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", identity.AttemptID)
	response, e := p.client.Do(r)
	if e != nil {
		return "unknown", ""
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return "rejected", ""
	}
	if response.StatusCode != 202 {
		return "unknown", ""
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(body) > 65536 {
		return "unknown", ""
	}
	var a struct {
		SchemaVersion     int    `json:"schemaVersion"`
		Status            string `json:"status"`
		ProviderRequestID string `json:"providerRequestId"`
	}
	if json.Unmarshal(body, &a) != nil || a.SchemaVersion != 1 || a.Status != "accepted" || strings.TrimSpace(a.ProviderRequestID) == "" || len(a.ProviderRequestID) > 128 {
		return "unknown", ""
	}
	return "accepted", a.ProviderRequestID
}
