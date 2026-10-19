// Command contract captures Java responses or verifies a candidate against a saved run.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"logan/servergo/internal/contract"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type runIndex struct {
	Version        int       `json:"version"`
	Evidence       string    `json:"evidence"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	Complete       bool      `json:"complete"`
	Cases          []string  `json:"cases"`
	CreatedAt      time.Time `json:"created_at"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("contract", flag.ContinueOnError)
	mode := f.String("mode", "capture", "capture or verify")
	base := f.String("base-url", "", "service URL including context prefix, e.g. http://127.0.0.1:8080/logan-web")
	manifest := f.String("manifest", "tests/contract/cases.json", "contract manifest")
	suite := f.String("suite", "safe", "safe or all; all requires test fixtures and write opt-in")
	out := f.String("out", "tests/contract/captures/java", "new capture directory (must not exist)")
	reference := f.String("reference", "tests/contract/captures/java", "baseline directory for verify")
	varsFile := f.String("vars", "", "local JSON string map for fixture substitutions")
	allow := f.Bool("allow-side-effects", false, "allow upload, lazy parsing and intentional error logging on isolated environments")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *mode != "capture" && *mode != "verify" {
		return fmt.Errorf("mode must be capture or verify")
	}
	if *suite != "safe" && *suite != "all" {
		return fmt.Errorf("suite must be safe or all")
	}
	if *base == "" {
		return fmt.Errorf("base-url is required; use an isolated Java/test deployment")
	}
	m, err := contract.LoadManifest(*manifest)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*manifest)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	vars := map[string]string{}
	if *varsFile != "" {
		b, err := os.ReadFile(*varsFile)
		if err != nil {
			return fmt.Errorf("cannot read variables file")
		}
		if json.Unmarshal(b, &vars) != nil {
			return fmt.Errorf("variables must be a JSON string map")
		}
	}
	var selected []contract.Case
	// Fail all missing fixtures and unsafe cases before sending any requests.
	for _, c := range m.Cases {
		if *suite == "safe" && c.Suite != "safe" {
			continue
		}
		if c.SideEffects && !*allow {
			return fmt.Errorf("case %s requires allow-side-effects", c.ID)
		}
		inputs := []string{c.Path, c.Body, c.BodyFile}
		for _, v := range c.Headers {
			inputs = append(inputs, v)
		}
		for _, v := range inputs {
			if _, err := contract.Expand(v, vars); err != nil {
				return fmt.Errorf("case %s: %w", c.ID, err)
			}
		}
		if c.BodyFile != "" {
			file, _ := contract.Expand(c.BodyFile, vars)
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("fixture file for %s unavailable", c.ID)
			}
		}
		selected = append(selected, c)
	}
	if len(selected) == 0 {
		return fmt.Errorf("empty selected suite")
	}
	baselines := map[string]contract.Snapshot{}
	if *mode == "capture" {
		if _, err := os.Stat(*out); err == nil {
			return fmt.Errorf("output directory already exists; select a new directory")
		}
		if err := os.MkdirAll(*out, 0700); err != nil {
			return err
		}
	} else {
		b, err := os.ReadFile(filepath.Join(*reference, "run.json"))
		if err != nil {
			return fmt.Errorf("baseline run index unavailable")
		}
		var idx runIndex
		if json.Unmarshal(b, &idx) != nil || !idx.Complete || idx.Version != 1 || idx.ManifestSHA256 != digest {
			return fmt.Errorf("baseline incomplete or manifest fingerprint mismatch")
		}
		available := map[string]bool{}
		for _, id := range idx.Cases {
			available[id] = true
		}
		for _, c := range selected {
			if !available[c.ID] {
				return fmt.Errorf("baseline run does not include case %s", c.ID)
			}
			snap, err := contract.LoadSnapshot(filepath.Join(*reference, c.ID+".json"))
			if err != nil {
				return fmt.Errorf("baseline missing case %s", c.ID)
			}
			if snap.CaseID != c.ID || snap.ManifestSHA256 != digest {
				return fmt.Errorf("case %s fingerprint mismatch", c.ID)
			}
			baselines[c.ID] = snap
		}
	}
	idx := runIndex{Version: 1, Evidence: "http-runtime", ManifestSHA256: digest, CreatedAt: time.Now().UTC()}
	for _, c := range selected {
		got, err := contract.Capture(context.Background(), *base, c, vars, *allow)
		if err != nil {
			return err
		}
		got.ManifestSHA256 = digest
		if *mode == "capture" {
			if c.ExpectedStatus != 0 && got.Status != c.ExpectedStatus {
				return fmt.Errorf("case %s: source-derived HTTP expectation mismatch (%d vs %d); investigate before accepting baseline", c.ID, got.Status, c.ExpectedStatus)
			}
			if len(c.ExpectedJSON) > 0 {
				if err := contract.CompareJSON(c.ExpectedJSON, got.Body); err != nil {
					return fmt.Errorf("case %s: source-derived JSON expectation mismatch; inspect isolated response", c.ID)
				}
			}
			if err := contract.SaveSnapshot(filepath.Join(*out, c.ID+".json"), got); err != nil {
				return err
			}
		} else {
			old := baselines[c.ID]
			if err := contract.CompareSnapshots(old, got, c.Comparison); err != nil {
				return fmt.Errorf("case %s: %w", c.ID, err)
			}
		}
		idx.Cases = append(idx.Cases, c.ID)
		fmt.Printf("PASS %s\n", c.ID)
	}
	if *mode == "capture" {
		idx.Complete = true
		b, _ := json.MarshalIndent(idx, "", "  ")
		if err := os.WriteFile(filepath.Join(*out, "run.json"), append(b, '\n'), 0600); err != nil {
			return err
		}
	}
	fmt.Printf("%s: %d cases (%s suite)\n", strings.ToUpper(*mode), len(selected), *suite)
	return nil
}
