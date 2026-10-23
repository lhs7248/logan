package workbench

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"logan/servergo/internal/workbench/connectors"
)

const Base = "/logan-web/api/workbench/v1"

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Demo         bool   `json:"demo"`
	ModelEnabled bool   `json:"modelEnabled"`
}
type Token struct {
	Token       string            `json:"token"`
	Actor       string            `json:"actor"`
	Projects    map[string]string `json:"projects"`
	Permissions []string          `json:"permissions"`
}
type Config struct {
	SDKClients    []SDKClient `json:"sdkClients"`
	MaxSDKBytes   int64       `json:"maxSDKBytes"`
	Dir           string
	Store         Persistence
	Projects      []Project `json:"projects"`
	Tokens        []Token   `json:"tokens"`
	Source        *connectors.Source
	Model         *connectors.Model
	Protocols     *connectors.Protocols
	Legacy        *connectors.Legacy
	MaxFileBytes  int64
	MaxBatchBytes int64
	MaxEvents     int
	MaxStateBytes int64
	Retention     time.Duration
}
type Server struct {
	mu          sync.Mutex
	cfg         Config
	state       *State
	store       Persistence
	ctx         context.Context
	cancel      context.CancelFunc
	jobs        sync.WaitGroup
	cancels     map[string]context.CancelFunc
	uploadSlots chan struct{}
	closed      bool
	unhealthy   error
	epoch       uint64
}
type apiError struct {
	code, message string
	status        int
}

func fail(status int, code, message string) *apiError { return &apiError{code, message, status} }
func Open(cfg Config) (*Server, error) {
	if err := ValidateSDKConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.MaxSDKBytes == 0 {
		cfg.MaxSDKBytes = 32 << 20
	}
	if cfg.MaxFileBytes == 0 {
		cfg.MaxFileBytes = 8 << 20
	}
	if cfg.MaxBatchBytes == 0 {
		cfg.MaxBatchBytes = 16 << 20
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 100000
	}
	if cfg.MaxStateBytes == 0 {
		cfg.MaxStateBytes = 128 << 20
	}
	if cfg.Retention == 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	var err error
	if cfg.Store == nil {
		cfg.Store, err = NewFileStore(cfg.Dir)
		if err != nil {
			return nil, err
		}
	}
	state, err := cfg.Store.Load()
	if err != nil {
		cfg.Store.Close()
		return nil, err
	}
	if cfg.Source == nil {
		cfg.Source, err = connectors.NewSource(connectors.SourceConfig{})
		if err != nil {
			return nil, err
		}
	}
	if cfg.Model == nil {
		cfg.Model, err = connectors.NewModel(connectors.ModelConfig{})
		if err != nil {
			return nil, err
		}
	}
	if cfg.Protocols == nil {
		cfg.Protocols, err = connectors.NewProtocols(connectors.ProtocolConfig{})
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, state: state, store: cfg.Store, ctx: ctx, cancel: cancel, cancels: map[string]context.CancelFunc{}, uploadSlots: make(chan struct{}, 2)}
	// An exclusive storage lease fences all workers. On restart unfinished work is
	// terminally interrupted; users may explicitly start a fresh attempt.
	changed := false
	for _, kind := range []string{"parseRuns", "turns"} {
		for _, r := range s.state.bucket(kind) {
			if r["status"] == "queued" || r["status"] == "running" {
				r["status"] = "interrupted"
				if kind == "parseRuns" {
					r["status"] = "failed"
				}
				r["error"] = Record{"code": "PROCESS_INTERRUPTED", "message": "Server stopped before this attempt completed"}
				changed = true
			}
		}
	}
	s.purgeExpired()
	if changed || len(state.Objects) > 0 {
		if err = s.store.Save(s.state); err != nil {
			cancel()
			cfg.Store.Close()
			return nil, err
		}
	}
	s.startReaper()
	return s, nil
}
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.jobs.Wait()
	return s.store.Close()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	trace := id()
	w.Header().Set("X-Trace-ID", trace)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == SDKNativePath {
		s.serveSDK(w, r, trace)
		return
	}
	var principal *Token
	supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if supplied != "" && supplied != r.Header.Get("Authorization") {
		for i := range s.cfg.Tokens {
			if subtle.ConstantTimeCompare([]byte(supplied), []byte(s.cfg.Tokens[i].Token)) == 1 {
				principal = &s.cfg.Tokens[i]
				break
			}
		}
	}
	if principal == nil {
		writeError(w, trace, fail(401, "UNAUTHENTICATED", "A configured API token is required"))
		return
	}
	project := r.Header.Get("X-Project-ID")
	if project == "" && r.URL.Path == Base+"/session" {
		for _, p := range s.cfg.Projects {
			if principal.Projects[p.ID] != "" {
				project = p.ID
				break
			}
		}
	}
	role := principal.Projects[project]
	if role == "" {
		writeError(w, trace, fail(403, "PROJECT_FORBIDDEN", "Project access denied"))
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && role == "viewer" {
		writeError(w, trace, fail(403, "ROLE_FORBIDDEN", "Investigator role is required"))
		return
	}
	if r.Method == "POST" && r.URL.Path == Base+"/imports" {
		select {
		case s.uploadSlots <- struct{}{}:
			defer func() { <-s.uploadSlots }()
		default:
			writeError(w, trace, fail(429, "IMPORT_CAPACITY", "Two imports are active; retry after one completes"))
			return
		}
	}
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, 32<<20+1))
		if err != nil || len(body) > 32<<20 {
			writeError(w, trace, fail(413, "BODY_TOO_LARGE", "Request exceeds 32 MiB"))
			return
		}
	}
	if !strings.HasPrefix(r.URL.Path, Base+"/") {
		writeError(w, trace, fail(404, "NOT_FOUND", "Unknown workbench route"))
		return
	}
	mutation := r.Method != "GET" && r.Method != "HEAD"
	key := strings.ToLower(r.Header.Get("Idempotency-Key"))
	if mutation {
		if problem := keyProblem(key); problem != nil {
			writeError(w, trace, problem)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.unhealthy != nil {
		writeError(w, trace, fail(503, "STORE_UNAVAILABLE", "Persistent store is unavailable"))
		return
	}
	receiptKey := project + "\x00" + principal.Actor + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + key
	hash := digest(append([]byte(r.Header.Get("If-Match")+"\x00"), normalizedBody(r.Header.Get("Content-Type"), body)...))
	q := request{s: s, r: r, project: project, role: role, actor: principal, body: body, trace: trace}
	if mutation {
		if prev, ok := s.state.Idempotency[receiptKey]; ok {
			if prev.Hash != hash {
				writeError(w, trace, fail(409, "IDEMPOTENCY_CONFLICT", "This key was used with different input"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(prev.Status)
			var replay Record
			_ = json.Unmarshal(prev.Body, &replay)
			replay["data"] = q.redact(replay["data"])
			replayBody, _ := json.Marshal(replay)
			_, _ = w.Write(replayBody)
			return
		}
	}
	before := s.state
	if mutation {
		s.state = clone(s.state)
		s.purgeExpired()
	}
	value, status, ae := q.route()
	if ae != nil {
		if mutation && !q.staleExternal {
			s.state = before
		}
		writeError(w, trace, ae)
		return
	}
	if q.download != nil {
		w.Header().Set("Content-Type", q.contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", q.filename))
		w.WriteHeader(status)
		_, _ = w.Write(q.download)
		return
	}
	operationID := ""
	if mutation {
		op := s.state.add("operations", project, Record{"actor": principal.Actor, "key": key, "operation": r.Method + " " + r.URL.Path, "status": "completed", "resourceId": record(value)["id"], "result": value, "expiresAt": time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)})
		operationID = str(op["id"])
	}
	response, _ := json.Marshal(Record{"data": q.redact(value), "meta": Record{"traceId": trace, "operationId": operationID}})
	if mutation {
		s.epoch++
		s.state.Idempotency[receiptKey] = Receipt{hash, status, response}
		s.state.add("audit", project, Record{"actor": principal.Actor, "action": r.Method + " " + r.URL.Path, "traceId": trace, "result": "success"})
		stateBytes, _ := json.Marshal(s.state)
		if int64(len(stateBytes)) > s.cfg.MaxStateBytes {
			s.state = before
			writeError(w, trace, fail(507, "STORE_CAPACITY", "Cumulative workbench state capacity reached; no changes were committed"))
			return
		}
		if err = s.store.Save(s.state); err != nil {
			s.state = before
			s.unhealthy = err
			writeError(w, trace, fail(503, "STORE_UNAVAILABLE", "Commit failed; service stopped accepting operations"))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(response)
	for _, start := range q.afterCommit {
		start()
	}
}
func writeError(w http.ResponseWriter, trace string, e *apiError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(Record{"error": Record{"code": e.code, "message": e.message}, "meta": Record{"traceId": trace}})
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

type request struct {
	s                     *Server
	r                     *http.Request
	project, role, trace  string
	actor                 *Token
	body                  []byte
	afterCommit           []func()
	download              []byte
	filename, contentType string
	staleExternal         bool
}

func (q *request) input() (Record, *apiError) {
	r := Record{}
	if len(q.body) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(q.body, &r); err != nil || r == nil {
		return nil, fail(400, "INVALID_JSON", "Expected a JSON object")
	}
	return r, nil
}
func (q *request) get(kind, id string) (Record, *apiError) {
	r := q.s.state.bucket(kind)[id]
	if r == nil || r["projectId"] != q.project {
		return nil, fail(404, "NOT_FOUND", "Object not found in this project")
	}
	return r, nil
}
func (q *request) list(kind string) []Record {
	out := []Record{}
	for _, r := range q.s.state.bucket(kind) {
		if r["projectId"] == q.project {
			if kind == "sdkUploads" {
				out = append(out, publicSDKReceipt(r))
			} else {
				out = append(out, clone(r))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := str(out[i]["createdAt"]), str(out[j]["createdAt"])
		if a == b {
			return str(out[i]["id"]) < str(out[j]["id"])
		}
		return afterTimestamp(b, a)
	})
	return out
}
func (q *request) permission(p string) *apiError {
	for _, v := range q.actor.Permissions {
		if p == v {
			return nil
		}
	}
	return fail(403, "PERMISSION_FORBIDDEN", "Separate "+p+" permission is required")
}
func (q *request) match(r Record, field string) *apiError {
	v := strings.Trim(q.r.Header.Get("If-Match"), `"`)
	if v == "" {
		return fail(428, "REVISION_REQUIRED", "If-Match is required")
	}
	if v != fmt.Sprint(num(r[field])) {
		return fail(409, "REVISION_CONFLICT", "Object changed; reload before saving")
	}
	return nil
}
func (q *request) notify(target, kind, message string) {
	q.s.state.add("notifications", q.project, Record{"targetId": target, "kind": kind, "message": message, "read": false, "revision": 1})
}
func (q *request) capabilities() Record {
	platforms := []string{}
	for _, client := range q.s.cfg.SDKClients {
		if client.ProjectID != q.project {
			continue
		}
		for _, platform := range client.Platforms {
			if !contains(platforms, platform) {
				platforms = append(platforms, platform)
			}
		}
	}
	sort.Strings(platforms)
	return Record{"sdkReceiver": Record{"configured": len(platforms) > 0 && q.s.cfg.Protocols.NativeReady(), "path": SDKNativePath, "maxBytes": q.s.cfg.MaxSDKBytes, "platforms": platforms}, "storage": "durable", "uploads": true, "parsing": true, "protocols": Record{"native": Record{"configured": q.s.cfg.Protocols.NativeReady()}, "web": Record{"configured": q.s.cfg.Protocols.WebReady(), "encryptedConfigured": q.s.cfg.Protocols.WebEncryptedReady()}}, "source": Record{"configured": q.s.cfg.Source.Ready()}, "model": Record{"configured": q.modelReady(), "reason": "Model endpoint and per-project modelEnabled consent must both be configured"}, "collections": Record{"configured": false, "code": "CONNECTOR_UNCONFIGURED", "reason": "Aliyun protocol and credentials are deferred"}, "limits": Record{"fileBytes": q.s.cfg.MaxFileBytes, "batchBytes": q.s.cfg.MaxBatchBytes, "files": 10, "events": q.s.cfg.MaxEvents}, "legacy": Record{"available": q.s.cfg.Legacy != nil && q.s.cfg.Legacy.Ready(q.project), "reason": "Project mapping and read-only legacy adapter are not configured"}}
}
func connectorError(err error) *apiError {
	if errors.Is(err, connectors.ErrUnconfigured) {
		return fail(503, "CONNECTOR_UNCONFIGURED", "Connector is not configured")
	}
	if errors.Is(err, connectors.ErrInvalidRequest) {
		return fail(422, "SOURCE_INVALID", "Source identity or path is invalid")
	}
	if errors.Is(err, connectors.ErrLimitExceeded) {
		return fail(413, "CONNECTOR_LIMIT", "Connector resource limit exceeded")
	}
	return fail(502, "CONNECTOR_UNAVAILABLE", "Configured connector could not provide validated output")
}
