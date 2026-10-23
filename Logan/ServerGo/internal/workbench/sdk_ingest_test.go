package workbench

import (
	"bytes"
	"encoding/json"
	"logan/servergo/internal/workbench/connectors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSDKConfigurationAndAuth(t *testing.T) {
	cfg := Config{Dir: t.TempDir(), Projects: []Project{{ID: "p"}}, Tokens: []Token{{Token: "admin-secret-0000"}}, SDKClients: []SDKClient{{ID: "mobile", Token: "upload-secret-000", ProjectID: "p", AppID: "app", Environment: "test", Platforms: []string{"android"}}}}
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := httptest.NewRequest("POST", SDKNativePath, bytes.NewBufferString("bad"))
	r.Header.Set("Authorization", "Bearer admin-secret-0000")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("admin upload = %d", w.Code)
	}
	r = httptest.NewRequest("GET", Base+"/workspace", nil)
	r.Header.Set("Authorization", "Bearer upload-secret-000")
	r.Header.Set("X-Project-ID", "p")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("uploader workspace = %d", w.Code)
	}
	cfg.SDKClients[0].Token = cfg.Tokens[0].Token
	if _, e = Open(cfg); e == nil {
		t.Fatal("accepted credential collision")
	}
}

func sdkTestConfig(t *testing.T) Config {
	p, e := connectors.NewProtocols(connectors.ProtocolConfig{NativeKey: []byte("0123456789012345"), NativeIV: []byte("0123456789012345")})
	if e != nil {
		t.Fatal(e)
	}
	return Config{Dir: t.TempDir(), Protocols: p, MaxSDKBytes: 1024, Projects: []Project{{ID: "p"}}, SDKClients: []SDKClient{{ID: "mobile", Token: "upload-secret-000", ProjectID: "p", AppID: "app", Environment: "test", Platforms: []string{"android"}}}}
}
func sdkSend(s *Server, body, key string, headers map[string]string) (int, Record) {
	r := httptest.NewRequest("POST", SDKNativePath, bytes.NewBufferString(body))
	for k, v := range map[string]string{"X-Logan-Upload-Token": "upload-secret-000", "platform": "1", "appId": "app", "deviceId": "device", "fileDate": "2026-10-22", "X-Logan-Upload-ID": key} {
		r.Header.Set(k, v)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out Record
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func TestSDKReplayFailureExpiryAndAtomicCapacity(t *testing.T) {
	cfg := sdkTestConfig(t)
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range []map[string]string{{"appId": "other"}, {"X-Project-ID": "other"}, {"platform": "2"}, {"environment": "prod"}} {
		if code, _ := sdkSend(s, "bad", "scope", h); code != 403 {
			t.Fatalf("scope %v = %d", h, code)
		}
	}
	if code, _ := sdkSend(s, string(bytes.Repeat([]byte("x"), 1025)), "size", nil); code != 413 {
		t.Fatalf("size %d", code)
	}
	s.uploadSlots <- struct{}{}
	s.uploadSlots <- struct{}{}
	if code, _ := sdkSend(s, "bad", "slot", nil); code != 429 {
		t.Fatalf("slots %d", code)
	}
	<-s.uploadSlots
	<-s.uploadSlots
	s.mu.Lock()
	for i := 0; i < 2; i++ {
		s.state.add("parseRuns", "p", Record{"status": "queued"})
	}
	s.mu.Unlock()
	if code, _ := sdkSend(s, "bad", "capacity", nil); code != 429 {
		t.Fatalf("capacity %d", code)
	}
	s.mu.Lock()
	if len(s.state.bucket("assets")) != 0 || len(s.state.bucket("sdkUploads")) != 0 {
		t.Fatal("partial capacity mutation")
	}
	s.state.Objects["parseRuns"] = map[string]Record{}
	s.mu.Unlock()
	code, out := sdkSend(s, "malformed native", "stable", nil)
	if code != 200 {
		t.Fatalf("accept %d %v", code, out)
	}
	d := record(out["data"])
	rid, aid := str(d["runId"]), str(d["assetId"])
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		status := s.state.bucket("parseRuns")[rid]["status"]
		s.mu.Unlock()
		if status == "failed" {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	code, out = sdkSend(s, "malformed native", "stable", nil)
	if code != 200 || record(out["data"])["status"] != "failed" || record(out["data"])["assetId"] != aid {
		t.Fatalf("replay %d %v", code, out)
	}
	if code, _ = sdkSend(s, "changed", "stable", nil); code != 409 {
		t.Fatalf("conflict %d", code)
	}
	s.mu.Lock()
	q := request{s: s, project: "p"}
	_, _, ae := q.startParse(s.state.bucket("assets")[aid], Record{})
	if ae != nil {
		t.Fatal(ae)
	}
	if e = s.store.Save(s.state); e != nil {
		t.Fatal(e)
	}
	s.mu.Unlock()
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.mu.Lock()
	for _, run := range s.state.bucket("parseRuns") {
		if run["status"] != "failed" {
			t.Fatalf("unobservable restart %v", run)
		}
	}
	s.state.bucket("assets")[aid]["expiresAt"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	s.purgeExpired()
	s.mu.Unlock()
	if code, _ = sdkSend(s, "malformed native", "stable", nil); code != 410 {
		t.Fatalf("expired replay %d", code)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.bucket("assets")) != 1 || s.state.bucket("raw")[aid] != nil {
		t.Fatal("replay duplicated or retained expired raw")
	}
}

func TestSDKCommitFailureRollsBackAndPersistsNothing(t *testing.T) {
	cfg := sdkTestConfig(t)
	disk, e := NewFileStore(cfg.Dir)
	if e != nil {
		t.Fatal(e)
	}
	store := &failingPersistence{Persistence: disk}
	cfg.Store = store
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	store.fail = true
	if code, _ := sdkSend(s, "bad", "failure", nil); code != 503 {
		t.Fatalf("commit %d", code)
	}
	s.mu.Lock()
	if len(s.state.bucket("assets")) != 0 || len(s.state.bucket("sdkUploads")) != 0 || len(s.state.bucket("parseRuns")) != 0 {
		t.Fatal("partial failed commit")
	}
	s.mu.Unlock()
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	cfg.Store = nil
	s, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if len(s.state.bucket("assets")) != 0 || len(s.state.bucket("sdkUploads")) != 0 {
		t.Fatal("failed upload survived restart")
	}
}

func TestSDKReplayCannotCrossReboundProject(t *testing.T) {
	cfg := sdkTestConfig(t)
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	code, out := sdkSend(s, "malformed", "rebound", nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	original := record(out["data"])["assetId"]
	s.Close()
	cfg.Projects = append(cfg.Projects, Project{ID: "other"})
	cfg.SDKClients[0].ProjectID = "other"
	s, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	code, out = sdkSend(s, "malformed", "rebound", nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	if record(out["data"])["assetId"] == original {
		t.Fatal("rebound credential exposed previous project receipt")
	}
}

func TestSDKCapabilityIsProjectScoped(t *testing.T) {
	cfg := sdkTestConfig(t)
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := request{s: s, project: "other"}
	receiver := record(q.capabilities()["sdkReceiver"])
	if receiver["configured"] == true {
		t.Fatal("another project's credential must not advertise configured intake")
	}
	q.project = "p"
	receiver = record(q.capabilities()["sdkReceiver"])
	if receiver["configured"] != true {
		t.Fatal("configured project receiver missing")
	}
	platforms := stringsOf(receiver["platforms"])
	if len(platforms) != 1 || platforms[0] != "android" {
		t.Fatalf("unconfigured platform advertised: %v", platforms)
	}
}
