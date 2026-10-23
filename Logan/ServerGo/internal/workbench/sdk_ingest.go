package workbench

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const SDKNativePath = Base + "/sdk/native"

type SDKClient struct {
	ID          string   `json:"id"`
	Token       string   `json:"token"`
	ProjectID   string   `json:"projectId"`
	AppID       string   `json:"appId"`
	Environment string   `json:"environment"`
	Platforms   []string `json:"platforms"`
}

func ValidateSDKConfig(cfg Config) error {
	if cfg.MaxSDKBytes < 0 || cfg.MaxSDKBytes > 32<<20 {
		return errors.New("SDK byte limit must be between 1 and 32 MiB")
	}
	ids, tokens := map[string]bool{}, map[string]bool{}
	for _, t := range cfg.Tokens {
		tokens[t.Token] = true
	}
	for _, c := range cfg.SDKClients {
		project := false
		for _, p := range cfg.Projects {
			if p.ID == c.ProjectID {
				project = true
			}
		}
		if strings.TrimSpace(c.ID) == "" || ids[c.ID] || len(c.Token) < 16 || tokens[c.Token] || !project || strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.Environment) == "" || len(c.Platforms) == 0 {
			return errors.New("invalid SDK client identity, scope or credential collision")
		}
		seen := map[string]bool{}
		for _, p := range c.Platforms {
			if (p != "android" && p != "ios") || seen[p] {
				return errors.New("invalid SDK client platforms")
			}
			seen[p] = true
		}
		ids[c.ID] = true
		tokens[c.Token] = true
	}
	return nil
}

var sdkUploadID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func (s *Server) serveSDK(w http.ResponseWriter, r *http.Request, trace string) {
	reject := func(e *apiError) { writeError(w, trace, e) }
	var client *SDKClient
	supplied := r.Header.Get("X-Logan-Upload-Token")
	for i := range s.cfg.SDKClients {
		if supplied != "" && subtle.ConstantTimeCompare([]byte(supplied), []byte(s.cfg.SDKClients[i].Token)) == 1 {
			client = &s.cfg.SDKClients[i]
		}
	}
	if client == nil {
		reject(fail(401, "UNAUTHENTICATED", "An application upload credential is required"))
		return
	}
	if r.Method != "POST" {
		reject(fail(405, "METHOD_NOT_ALLOWED", "Use POST"))
		return
	}
	if !s.cfg.Protocols.NativeReady() {
		reject(fail(503, "CONNECTOR_UNCONFIGURED", "Native decoder key/IV are not configured"))
		return
	}
	platform := map[string]string{"1": "android", "2": "ios"}[r.Header.Get("platform")]
	if !contains(client.Platforms, platform) || platform == "" {
		reject(fail(403, "SDK_SCOPE", "Platform is outside the upload credential scope"))
		return
	}
	for _, pair := range [][2]string{{"appId", client.AppID}, {"X-Project-ID", client.ProjectID}, {"projectId", client.ProjectID}, {"environment", client.Environment}} {
		if v := r.Header.Get(pair[0]); v != "" && v != pair[1] {
			reject(fail(403, "SDK_SCOPE", "Upload metadata contradicts credential scope"))
			return
		}
	}
	device, date := r.Header.Get("deviceId"), r.Header.Get("fileDate")
	if strings.TrimSpace(device) == "" || len(device) > 512 || len(date) > 32 {
		reject(fail(422, "SDK_METADATA", "Bounded deviceId and fileDate are required"))
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		reject(fail(422, "SDK_METADATA", "fileDate must use YYYY-MM-DD"))
		return
	}
	uploadID := r.Header.Get("X-Logan-Upload-ID")
	if uploadID != "" && !sdkUploadID.MatchString(uploadID) {
		reject(fail(422, "SDK_UPLOAD_ID", "Upload ID must contain 1-128 safe characters"))
		return
	}
	build, bundle, version := r.Header.Get("buildVersion"), r.Header.Get("bundleVersion"), r.Header.Get("appVersion")
	if len(build) > 256 || len(bundle) > 256 || len(version) > 256 {
		reject(fail(422, "SDK_METADATA", "Version metadata exceeds limit"))
		return
	}
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		reject(fail(429, "IMPORT_CAPACITY", "Two uploads are active"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxSDKBytes+1))
	if err != nil || int64(len(body)) > s.cfg.MaxSDKBytes {
		reject(fail(413, "BODY_TOO_LARGE", "SDK body exceeds configured limit"))
		return
	}
	if len(body) == 0 {
		reject(fail(422, "EMPTY_FILE", "SDK body is empty"))
		return
	}
	meta := Record{"projectId": client.ProjectID, "clientId": client.ID, "device": device, "fileDate": date, "platform": platform, "app": client.AppID, "environment": client.Environment, "version": version, "buildVersion": build, "bundleVersion": bundle, "rawHash": digest(body)}
	fingerprint, _ := json.Marshal(meta)
	hash := digest(fingerprint)
	replayKey := uploadID
	if replayKey == "" {
		replayKey = hash
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.unhealthy != nil {
		reject(fail(503, "STORE_UNAVAILABLE", "Persistent store is unavailable"))
		return
	}
	for _, receipt := range s.state.bucket("sdkUploads") {
		if receipt["projectId"] == client.ProjectID && receipt["clientId"] == client.ID && receipt["uploadKey"] == replayKey {
			if receipt["fingerprint"] != hash {
				reject(fail(409, "SDK_UPLOAD_CONFLICT", "Upload ID was used with different metadata or bytes"))
				return
			}
			a := s.state.bucket("assets")[str(receipt["assetId"])]
			if a == nil || a["projectId"] != client.ProjectID || recordExpired(a) || recordExpired(receipt) {
				reject(fail(410, "SOURCE_EXPIRED", "Original upload expired"))
				return
			}
			s.writeSDK(w, trace, receipt)
			return
		}
	}
	before := s.state
	s.state = clone(before)
	s.purgeExpired()
	rollback := func(e *apiError) { s.state = before; reject(e) }
	// Store raw Native bytes directly: detection must never override declared Native.
	stored := 0
	for _, raw := range s.state.bucket("raw") {
		stored += num(raw["size"])
	}
	if stored+len(body) > 256<<20 {
		rollback(fail(507, "STORE_CAPACITY", "Raw storage capacity reached"))
		return
	}
	receipt := s.state.add("sdkUploads", client.ProjectID, Record{"clientId": client.ID, "uploadKey": replayKey, "fingerprint": hash, "status": "accepted", "device": device, "fileDate": date, "platform": platform})
	if platform == "ios" {
		build = bundle
	}
	a := s.state.add("assets", client.ProjectID, Record{"name": date + ".logan", "origin": "sdk", "declaredFormat": "native", "identitySource": "sdk_reported", "originalEncrypted": true, "sdkReceiptId": receipt["id"], "sdkClientId": client.ID, "provenance": Record{"receiptId": receipt["id"], "clientId": client.ID, "scopeSource": "configured_credential", "deviceSource": "sdk_reported", "buildSource": "sdk_reported", "platformSource": "sdk_reported"}, "platform": platform, "device": device, "fileDate": date, "app": client.AppID, "environment": client.Environment, "version": version, "build": build, "rawHash": digest(body), "size": len(body), "expiresAt": time.Now().Add(s.cfg.Retention).UTC().Format(time.RFC3339Nano), "revision": 1, "issueId": ""})
	s.state.bucket("raw")[str(a["id"])] = Record{"projectId": client.ProjectID, "content": base64.StdEncoding.EncodeToString(body), "size": len(body)}
	q := request{s: s, project: client.ProjectID}
	run, _, e := q.startParse(a, Record{"format": "native", "mode": "strict"})
	if e != nil {
		rollback(e)
		return
	}
	receipt["assetId"] = a["id"]
	receipt["runId"] = record(run)["id"]
	receipt["expiresAt"] = a["expiresAt"]
	b, _ := json.Marshal(s.state)
	if int64(len(b)) > s.cfg.MaxStateBytes {
		rollback(fail(507, "STORE_CAPACITY", "State capacity reached"))
		return
	}
	if err = s.store.Save(s.state); err != nil {
		s.state = before
		s.unhealthy = err
		reject(fail(503, "STORE_UNAVAILABLE", "Upload commit failed"))
		return
	}
	s.epoch++
	s.writeSDK(w, trace, receipt)
	for _, start := range q.afterCommit {
		start()
	}
}
func (s *Server) writeSDK(w http.ResponseWriter, trace string, receipt Record) {
	run := s.state.bucket("parseRuns")[str(receipt["runId"])]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(Record{"code": 200, "msg": "Upload accepted", "data": Record{"receiptId": receipt["id"], "assetId": receipt["assetId"], "runId": receipt["runId"], "status": run["status"], "receipt": publicSDKReceipt(receipt), "run": run}, "meta": Record{"traceId": trace}})
}
func publicSDKReceipt(r Record) Record {
	out := clone(r)
	delete(out, "fingerprint")
	delete(out, "uploadKey")
	return out
}
