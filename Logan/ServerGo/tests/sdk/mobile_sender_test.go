package sdk_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"logan/servergo/internal/workbench"
)

// Run the repository's actual uploaders against the same Go receiver used by
// the investigation test. Only Android scheduling and iOS file discovery are
// host substitutes; CLogan bytes, HTTP senders, receiver and parser are real.
func testMobileSenders(t *testing.T, module, fixture, base, admin, upload string) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(module))
	sdk := filepath.Join(root, "Example/Logan-Android/logan/src/main/java/com/dianping/logan")
	var androidAsset string
	check := func(t *testing.T, reply string, platform, device string) string {
		t.Helper()
		b, err := os.ReadFile(reply)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Code int
			Data map[string]any
		}
		if err = json.Unmarshal(b, &envelope); err != nil || envelope.Code != 200 {
			t.Fatalf("upload envelope: %v %s", err, b)
		}
		asset := envelope.Data["assetId"].(string)
		run := envelope.Data["runId"].(string)
		get := func(path string) map[string]any {
			t.Helper()
			req, _ := http.NewRequest("GET", base+workbench.Base+path, nil)
			req.Header.Set("Authorization", "Bearer "+admin)
			req.Header.Set("X-Project-ID", "sdk-integration")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("GET %s: %d", path, resp.StatusCode)
			}
			var data struct{ Data map[string]any }
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				t.Fatal(err)
			}
			return data.Data
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			r := get("/parse-runs/" + run)
			if r["status"] == "succeeded" {
				if r["eventCount"] != float64(4) {
					t.Fatal(r)
				}
				break
			}
			if r["status"] == "failed" || time.Now().After(deadline) {
				t.Fatal(r)
			}
			time.Sleep(10 * time.Millisecond)
		}
		rows := get("/parse-runs/" + run + "/events")["events"].([]any)
		found := false
		for _, row := range rows {
			if row.(map[string]any)["content"] == "TRIAD|002|AuthClient: POST /login timed out; requestId=triad-login-1" {
				found = true
			}
		}
		if !found {
			t.Fatal("actual mobile body lost exact SDK timeout record")
		}
		ws := get("/workspace")
		found = false
		for _, row := range ws["assets"].([]any) {
			a := row.(map[string]any)
			if a["id"] == asset {
				found = true
				if a["origin"] != "sdk" || a["platform"] != platform || a["device"] != device || a["issueId"] != "" {
					t.Fatalf("sender identity not preserved: %v", a)
				}
			}
		}
		if !found {
			t.Fatal("mobile asset not visible")
		}
		req, _ := http.NewRequest("GET", base+workbench.Base+"/assets/"+asset+"/raw", nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("X-Project-ID", "sdk-integration")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(fixture, "native.bin"))
		if err != nil || resp.StatusCode != 200 || !bytes.Equal(raw, want) {
			t.Fatal("mobile encrypted original changed")
		}
		return asset
	}
	t.Run("AndroidActualSender", func(t *testing.T) {
		javac, err := exec.LookPath("javac")
		if err != nil {
			t.Skip("JDK needed for actual Android sender host test")
		}
		java, err := exec.LookPath("java")
		if err != nil {
			t.Skip("Java runtime needed")
		}
		classes := t.TempDir()
		args := []string{"-d", classes, filepath.Join(root, "tests/mobile/java/android/util/Log.java"), filepath.Join(root, "tests/mobile/java/com/dianping/logan/Logan.java"), filepath.Join(root, "tests/mobile/java/com/dianping/logan/SendLogRunnable.java"), filepath.Join(sdk, "SendLogDefaultRunnable.java"), filepath.Join(sdk, "SendLogCallback.java"), filepath.Join(root, "docs/examples/mobile/WorkbenchLoganUpload.java"), filepath.Join(root, "tests/mobile/java/WorkbenchReceiverUpload.java")}
		if out, err := exec.Command(javac, args...).CombinedOutput(); err != nil {
			t.Fatalf("actual Android uploader compile: %v %s", err, out)
		}
		reply := filepath.Join(classes, "receipt.json")
		cmd := exec.Command(java, "-cp", classes, "WorkbenchReceiverUpload", filepath.Join(fixture, "native.bin"), base+workbench.SDKNativePath, "triad-android-host-1", reply)
		cmd.Env = append(os.Environ(), "WORKBENCH_SDK_TOKEN="+upload)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Android actual sender: %v %s", err, out)
		}
		androidAsset = check(t, reply, "android", "triad-device")
	})
	t.Run("IOSActualUploader", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("macOS and Xcode required for actual Objective-C uploader")
		}
		xcrun, err := exec.LookPath("xcrun")
		if err != nil {
			t.Skip("Xcode tools required")
		}
		dir := t.TempDir()
		binary := filepath.Join(dir, "upload")
		reply := filepath.Join(dir, "receipt.json")
		args := []string{"clang", "-fobjc-arc", "-fblocks", "-Wno-deprecated-declarations", "-Wno-nullability-completeness", "-framework", "Cocoa", "-I", filepath.Join(root, "Logan/Clogan"), filepath.Join(root, "tests/mobile/receiver_upload.m"), "-o", binary}
		if out, err := exec.Command(xcrun, args...).CombinedOutput(); err != nil {
			t.Fatalf("actual iOS uploader compile: %v %s", err, out)
		}
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), "WORKBENCH_SDK_TOKEN="+upload, "LOGAN_RECEIVER_FIXTURE="+filepath.Join(fixture, "native.bin"), "LOGAN_RECEIVER_URL="+base+workbench.SDKNativePath, "LOGAN_RECEIVER_REPLY="+reply)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("iOS actual uploader: %v %s", err, out)
		}
		check(t, reply, "ios", "triad-ios-host")
	})
	return androidAsset
}
