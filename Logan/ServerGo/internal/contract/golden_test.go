package contract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordedJavaControllerBaseline(t *testing.T) {
	root := "../../tests/contract"
	raw, err := os.ReadFile(filepath.Join(root, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	m, err := LoadManifest(filepath.Join(root, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "golden/run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Complete       bool     `json:"complete"`
		Evidence       string   `json:"evidence"`
		ManifestSHA256 string   `json:"manifest_sha256"`
		Cases          []string `json:"cases"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if !run.Complete || run.Evidence != "java-controller-isolated" || run.ManifestSHA256 != digest {
		t.Fatal("invalid Java evidence index")
	}
	ids := map[string]bool{}
	for _, id := range run.Cases {
		ids[id] = true
	}
	count := 0
	for _, c := range m.Cases {
		if c.Suite != "safe" {
			continue
		}
		count++
		if !ids[c.ID] {
			t.Errorf("missing recorded case %s", c.ID)
			continue
		}
		s, err := LoadSnapshot(filepath.Join(root, "golden", c.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if s.CaseID != c.ID || s.CapturedAt.IsZero() || s.ManifestSHA256 != digest || s.Status != c.ExpectedStatus {
			t.Errorf("invalid evidence for %s", c.ID)
		}
		if len(c.ExpectedJSON) > 0 {
			if err := CompareJSON(c.ExpectedJSON, s.Body); err != nil {
				t.Errorf("%s: %v", c.ID, err)
			}
		}
	}
	if count != 14 || len(ids) != count {
		t.Fatal("recorded case coverage changed")
	}
}
func TestManifestRejectsEscapingSnapshotNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cases.json")
	data := `{"version":1,"routes":[{"id":"one"}],"cases":[{"id":"../../escape","route":"one","method":"GET","path":"/one","comparison":"json","suite":"safe"}]}`
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("unsafe snapshot file name accepted")
	}
}
