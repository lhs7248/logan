package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureThenVerify(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "cases.json")
	out := filepath.Join(dir, "baseline")
	data := `{"version":1,"routes":[{"id":"one"}],"cases":[{"id":"one","route":"one","method":"GET","path":"/one","comparison":"json","suite":"safe","expected_status":200}]}`
	if err := os.WriteFile(manifest, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	body := `{"code":200,"msg":null,"data":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	args := []string{"--base-url", srv.URL, "--manifest", manifest, "--out", out}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	verify := []string{"--mode", "verify", "--base-url", srv.URL, "--manifest", manifest, "--reference", out}
	if err := run(verify); err != nil {
		t.Fatal(err)
	}
	body = `{"code":200,"msg":null,"data":null}`
	if err := run(verify); err == nil {
		t.Fatal("null/empty mismatch accepted")
	}
	if err := run(args); err == nil {
		t.Fatal("existing evidence overwritten")
	}
}
func TestRefusesMissingFixturesBeforeSendingRequests(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cases.json")
	data := `{"version":1,"routes":[{"id":"one"}],"cases":[{"id":"one","route":"one","method":"GET","path":"/one/${TASK_ID}","comparison":"json","suite":"safe"}]}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--base-url", "http://127.0.0.1:1", "--manifest", file}); err == nil {
		t.Fatal("missing fixture accepted")
	}
}

func TestVerifyMissingEvidenceSendsNoRequests(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "cases.json")
	reference := filepath.Join(dir, "baseline")
	if err := os.Mkdir(reference, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"version":1,"routes":[{"id":"one"}],"cases":[{"id":"one","route":"one","method":"GET","path":"/one","comparison":"json","suite":"safe"}]}`)
	if err := os.WriteFile(manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	idx := runIndex{Version: 1, Evidence: "http-runtime", Complete: true, ManifestSHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	data, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(reference, "run.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	if err := run([]string{"--mode", "verify", "--manifest", manifest, "--reference", reference, "--base-url", srv.URL}); err == nil {
		t.Fatal("incomplete baseline accepted")
	}
	if hits != 0 {
		t.Fatal("sent request before validating reference evidence")
	}
}
