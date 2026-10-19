// Package contract describes and captures legacy HTTP behavior. It is not used by the server.
package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Route struct {
	ID              string `json:"id"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Source          string `json:"source"`
	SourceSHA256    string `json:"source_sha256"`
	Notes           string `json:"notes"`
	Evidence        string `json:"evidence"`
	RuntimeVerified bool   `json:"runtime_verified"`
}
type Case struct {
	ID             string            `json:"id"`
	Route          string            `json:"route"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	BodyFile       string            `json:"body_file,omitempty"`
	Comparison     string            `json:"comparison"`
	SideEffects    bool              `json:"side_effects"`
	Suite          string            `json:"suite"`
	Scenario       string            `json:"scenario"`
	ExpectedStatus int               `json:"expected_status,omitempty"`
	ExpectedJSON   json.RawMessage   `json:"expected_json,omitempty"`
}
type Manifest struct {
	Version int     `json:"version"`
	Status  string  `json:"status"`
	Routes  []Route `json:"routes"`
	Cases   []Case  `json:"cases"`
}

func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.Version != 1 {
		return m, fmt.Errorf("unsupported manifest version")
	}
	safeID := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	ids := map[string]bool{}
	routes := map[string]bool{}
	for _, r := range m.Routes {
		if r.ID == "" || routes[r.ID] {
			return m, fmt.Errorf("duplicate or empty route ID")
		}
		routes[r.ID] = true
	}
	for _, c := range m.Cases {
		if !safeID.MatchString(c.ID) || ids[c.ID] || !routes[c.Route] {
			return m, fmt.Errorf("invalid or duplicate case ID %q", c.ID)
		}
		if c.Comparison != "json" && c.Comparison != "bytes" {
			return m, fmt.Errorf("invalid comparison for %s", c.ID)
		}
		if c.Body != "" && c.BodyFile != "" {
			return m, fmt.Errorf("multiple bodies for %s", c.ID)
		}
		ids[c.ID] = true
	}
	return m, nil
}

var variablePattern = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

func Expand(input string, vars map[string]string) (string, error) {
	var missing string
	out := variablePattern.ReplaceAllStringFunc(input, func(s string) string {
		key := s[2 : len(s)-1]
		v, ok := vars[key]
		if !ok {
			missing = key
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("missing fixture variable %s", missing)
	}
	return out, nil
}
func ScanJavaRoutes(dir string) ([]Route, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.java"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Java controllers at %s", dir)
	}
	baseRE := regexp.MustCompile(`@RequestMapping\("([^"]+)"\)`)
	methodRE := regexp.MustCompile(`@(Get|Post)Mapping\("([^"]+)"\)`)
	var routes []Route
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		base := baseRE.FindSubmatch(b)
		if len(base) == 0 {
			continue
		}
		for _, m := range methodRE.FindAllSubmatch(b, -1) {
			routes = append(routes, Route{Method: strings.ToUpper(string(m[1])), Path: string(base[1]) + string(m[2])})
		}
	}
	return routes, nil
}
