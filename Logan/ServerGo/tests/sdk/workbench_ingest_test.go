package sdk_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"logan/servergo/internal/workbench"
	"logan/servergo/internal/workbench/connectors"
)

// This crosses the public CLogan API and actual local HTTP receiver. It does
// not represent an Android/iOS device or a configured production provider.
func TestNativeSDKWorkbenchHTTPChain(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for HTTP acceptance")
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("C compiler required for actual CLogan output")
	}
	module, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "fixture")
	cmd := exec.Command(python, filepath.Join(module, "script/generate-sdk-workbench-fixture.py"), "--output", fixture)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("CLogan producer: %v %s", err, out)
	}
	protocols, err := connectors.NewProtocols(connectors.ProtocolConfig{NativeKey: []byte("0123456789012345"), NativeIV: []byte("0123456789012345")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := workbench.Config{Dir: t.TempDir(), Protocols: protocols,
		Projects:   []workbench.Project{{ID: "sdk-integration", Name: "Synthetic SDK", Demo: true}},
		Tokens:     []workbench.Token{{Token: "sdk-test-admin-only-0000", Actor: "sdk-acceptance", Projects: map[string]string{"sdk-integration": "admin"}, Permissions: []string{"raw", "export", "source", "model"}}},
		SDKClients: []workbench.SDKClient{{ID: "sdk-test", Token: "sdk-test-upload-only-0000", ProjectID: "sdk-integration", AppID: "com.hxl.xlb.test", Environment: "isolated", Platforms: []string{"android", "ios"}}}}
	s, err := workbench.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	androidAsset := testMobileSenders(t, module, fixture, server.URL, cfg.Tokens[0].Token, cfg.SDKClients[0].Token)
	report := filepath.Join(t.TempDir(), "report.json")
	cmd = exec.Command(python, filepath.Join(module, "script/verify-sdk-workbench.py"), "--base", server.URL+workbench.Base, "--project", "sdk-integration", "--native", filepath.Join(fixture, "native.bin"), "--report", report, "--upload-id", "triad-android-host-1")
	cmd.Env = append(os.Environ(), "WORKBENCH_ACCEPTANCE_TOKEN=sdk-test-admin-only-0000", "WORKBENCH_SDK_TOKEN=sdk-test-upload-only-0000")
	out, runErr := cmd.CombinedOutput()
	server.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("CLogan -> HTTP -> investigation: %v %s", runErr, out)
	}
	var result struct {
		Passed  bool
		Objects map[string]string
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &result); err != nil || !result.Passed {
		t.Fatalf("acceptance report: %v %s", err, b)
	}
	if androidAsset != "" && result.Objects["assetId"] != androidAsset {
		t.Fatal("business flow did not use actual Android uploaded asset")
	}
	// Reopening the exact store must retain the completed investigation objects.
	s, err = workbench.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, path := range []string{"/parse-runs/" + result.Objects["runId"], "/issues/" + result.Objects["issueId"], "/workspace"} {
		r := httptest.NewRequest("GET", workbench.Base+path, nil)
		r.Header.Set("Authorization", "Bearer sdk-test-admin-only-0000")
		r.Header.Set("X-Project-ID", "sdk-integration")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("reopened %s: %d %s", path, w.Code, w.Body.String())
		}
		var envelope struct{ Data map[string]any }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		data := envelope.Data
		if path == "/parse-runs/"+result.Objects["runId"] && (data["status"] != "succeeded" || data["eventCount"] != float64(4)) {
			t.Fatalf("completed run lost: %v", data)
		}
		if path == "/issues/"+result.Objects["issueId"] {
			ids, _ := data["evidenceIds"].([]any)
			found := false
			for _, id := range ids {
				if id == result.Objects["evidenceId"] {
					found = true
				}
			}
			if !found {
				t.Fatal("issue lost its exact log evidence")
			}
		}
		if path == "/workspace" {
			for bucket, key := range map[string]string{"assets": "assetId", "evidence": "evidenceId", "verifications": "verificationId", "updates": "updateId", "sdkUploads": "receiptId"} {
				found := false
				rows, _ := data[bucket].([]any)
				for _, row := range rows {
					if row.(map[string]any)["id"] == result.Objects[key] {
						found = true
					}
				}
				if !found {
					t.Fatalf("reopen lost %s %s", bucket, result.Objects[key])
				}
			}
		}
	}
}
