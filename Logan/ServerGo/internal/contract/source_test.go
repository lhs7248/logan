package contract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestSourceFingerprint(t *testing.T) {
	m, err := LoadManifest("../../tests/contract/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range m.Routes {
		b, err := os.ReadFile(filepath.Join("../../../Server", r.Source))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != r.SourceSHA256 {
			t.Errorf("Java source changed for %s; re-review contract", r.ID)
		}
	}
}

func TestCompleteSourceLock(t *testing.T) {
	data, err := os.ReadFile("../../tests/contract/java-source-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Version int `json:"version"`
		Files   []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != 1 || len(lock.Files) < 65 {
		t.Fatal("incomplete source lock")
	}
	seen := map[string]bool{}
	for _, f := range lock.Files {
		b, err := os.ReadFile(filepath.Join("../../../Server", f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != f.SHA256 {
			t.Errorf("source changed: %s", f.Path)
		}
		seen[f.Path] = true
	}
	err = filepath.WalkDir("../../../Server/src/main/java", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".java" {
			rel, _ := filepath.Rel("../../../Server", path)
			if !seen[rel] {
				t.Errorf("new Java source not locked: %s", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
