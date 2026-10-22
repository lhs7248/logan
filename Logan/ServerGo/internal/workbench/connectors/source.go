// Package connectors reads explicitly configured sources and model providers.
// It never guesses repository/version associations or substitutes synthetic answers.
package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUnconfigured    = errors.New("CONNECTOR_UNCONFIGURED")
	ErrInvalidRequest  = errors.New("CONNECTOR_INVALID_REQUEST")
	ErrUnavailable     = errors.New("CONNECTOR_UNAVAILABLE")
	ErrLimitExceeded   = errors.New("CONNECTOR_LIMIT_EXCEEDED")
	ErrInvalidResponse = errors.New("CONNECTOR_INVALID_RESPONSE")
)

type SourceConfig struct {
	Repositories                 map[string]string
	AllowedExtensions            []string
	Timeout                      time.Duration
	MaxFileBytes, MaxOutputBytes int64
	MaxLines                     int
}
type Source struct {
	config       SourceConfig
	repositories map[string]string
	extensions   map[string]bool
}
type SourceRequest struct {
	RepoID, Commit, Path string
	Start, End           int
}
type Fragment struct {
	RepoID   string `json:"repoId"`
	Commit   string `json:"commit"`
	Path     string `json:"path"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Content  string `json:"content"`
	Hash     string `json:"hash"`
	BlobHash string `json:"blobHash"`
	Verified bool   `json:"verified"`
}
type DiffRequest struct{ RepoID, LeftCommit, RightCommit, Path string }
type Diff struct {
	RepoID      string `json:"repoId"`
	LeftCommit  string `json:"leftCommit"`
	RightCommit string `json:"rightCommit"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	Hash        string `json:"hash"`
}

func NewSource(c SourceConfig) (*Source, error) {
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		c.Timeout = 5 * time.Second
	}
	if c.MaxFileBytes <= 0 || c.MaxFileBytes > 4<<20 {
		c.MaxFileBytes = 1 << 20
	}
	if c.MaxOutputBytes <= 0 || c.MaxOutputBytes > 1<<20 {
		c.MaxOutputBytes = 128 << 10
	}
	if c.MaxLines <= 0 || c.MaxLines > 1000 {
		c.MaxLines = 200
	}
	s := &Source{config: c, repositories: map[string]string{}, extensions: map[string]bool{}}
	for id, p := range c.Repositories {
		if id == "" || !filepath.IsAbs(p) {
			return nil, ErrInvalidRequest
		}
		clean := filepath.Clean(p)
		resolved, e := filepath.EvalSymlinks(clean)
		if e != nil || resolved != clean {
			return nil, ErrInvalidRequest
		}
		info, e := os.Stat(clean)
		if e != nil || !info.IsDir() {
			return nil, ErrInvalidRequest
		}
		s.repositories[id] = clean
	}
	if len(c.AllowedExtensions) == 0 {
		c.AllowedExtensions = []string{".go", ".java", ".kt", ".swift", ".m", ".mm", ".h", ".c", ".cpp", ".js", ".ts", ".jsx", ".tsx", ".json", ".xml", ".yaml", ".yml", ".md", ".txt", ".css", ".html", ".gradle", ".properties", ".sql", ".proto"}
	}
	for _, ext := range c.AllowedExtensions {
		if !strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, "/\\\x00") {
			return nil, ErrInvalidRequest
		}
		s.extensions[strings.ToLower(ext)] = true
	}
	return s, nil
}
func (s *Source) Ready() bool { return s != nil && len(s.repositories) > 0 }

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (s *Source) validate(repo, commit, p string) error {
	if !s.Ready() {
		return ErrUnconfigured
	}
	if _, ok := s.repositories[repo]; !ok {
		return ErrInvalidRequest
	}
	if !commitPattern.MatchString(commit) {
		return ErrInvalidRequest
	}
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || strings.ContainsAny(p, "\\\x00\r\n\t") || path.IsAbs(p) || path.Clean(p) != p {
		return ErrInvalidRequest
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || strings.HasPrefix(part, "-") || strings.EqualFold(part, ".git") {
			return ErrInvalidRequest
		}
	}
	if !s.extensions[strings.ToLower(path.Ext(p))] {
		return ErrInvalidRequest
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	max      int64
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(b.buffer.Len()+len(p)) > b.max {
		b.exceeded = true
		return 0, ErrLimitExceeded
	}
	return b.buffer.Write(p)
}
func (s *Source) git(ctx context.Context, repo string, limit int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	base := []string{"--no-pager", "--no-replace-objects", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-C", s.repositories[repo]}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	// Avoid caller-controlled Git environment, external helpers and inherited credentials.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	out := &boundedBuffer{max: limit}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	e := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out.exceeded {
		return nil, ErrLimitExceeded
	}
	if e != nil {
		return nil, ErrUnavailable
	}
	return out.buffer.Bytes(), nil
}

// ValidateCommit validates an operator-selected immutable mapping target without
// reading source content. Build/app association remains the caller's responsibility.
func (s *Source) ValidateCommit(ctx context.Context, repoID, commit string) error {
	if !s.Ready() {
		return ErrUnconfigured
	}
	if _, ok := s.repositories[repoID]; !ok {
		return ErrInvalidRequest
	}
	if !commitPattern.MatchString(commit) {
		return ErrInvalidRequest
	}
	typ, err := s.git(ctx, repoID, 128, "cat-file", "-t", commit)
	if err != nil {
		return err
	}
	if string(typ) != "commit\n" {
		return ErrInvalidRequest
	}
	return nil
}
func (s *Source) blob(ctx context.Context, repo, commit, p string) (string, error) {
	if err := s.ValidateCommit(ctx, repo, commit); err != nil {
		return "", err
	}
	// Inspect each historical tree component, rejecting symlinks/submodules even
	// when the working copy differs from the immutable commit being read.
	parts := strings.Split(p, "/")
	var blob string
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		b, e := s.git(ctx, repo, 8192, "ls-tree", "-z", commit, "--", prefix)
		if e != nil {
			return "", e
		}
		fields := strings.SplitN(string(b), "\t", 2)
		if len(fields) != 2 {
			return "", ErrInvalidRequest
		}
		meta := strings.Fields(fields[0])
		if len(meta) != 3 || strings.TrimSuffix(fields[1], "\x00") != prefix {
			return "", ErrInvalidRequest
		}
		if i < len(parts)-1 {
			if meta[0] != "040000" {
				return "", ErrInvalidRequest
			}
		} else {
			if meta[0] != "100644" && meta[0] != "100755" {
				return "", ErrInvalidRequest
			}
			blob = meta[2]
		}
	}
	return blob, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Source) Read(ctx context.Context, r SourceRequest) (Fragment, error) {
	if !s.Ready() {
		return Fragment{}, ErrUnconfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if e := s.validate(r.RepoID, r.Commit, r.Path); e != nil {
		return Fragment{}, e
	}
	if r.Start == 0 {
		r.Start = 1
	}
	if r.End == 0 {
		r.End = r.Start + s.config.MaxLines - 1
	}
	if r.Start < 1 || r.End < r.Start || r.End-r.Start+1 > s.config.MaxLines {
		return Fragment{}, ErrInvalidRequest
	}
	blob, e := s.blob(ctx, r.RepoID, r.Commit, r.Path)
	if e != nil {
		return Fragment{}, e
	}
	b, e := s.git(ctx, r.RepoID, s.config.MaxFileBytes, "cat-file", "blob", blob)
	if e != nil {
		return Fragment{}, e
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return Fragment{}, ErrInvalidRequest
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if r.Start > len(lines) {
		return Fragment{}, ErrInvalidRequest
	}
	if r.End > len(lines) {
		r.End = len(lines)
	}
	snippet := bytes.Join(lines[r.Start-1:r.End], nil)
	if int64(len(snippet)) > s.config.MaxOutputBytes {
		return Fragment{}, ErrLimitExceeded
	}
	return Fragment{RepoID: r.RepoID, Commit: r.Commit, Path: r.Path, Start: r.Start, End: r.End, Content: string(snippet), Hash: digest(snippet), BlobHash: blob, Verified: true}, nil
}
func (s *Source) Diff(ctx context.Context, r DiffRequest) (Diff, error) {
	if !s.Ready() {
		return Diff{}, ErrUnconfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	for _, commit := range []string{r.LeftCommit, r.RightCommit} {
		if e := s.validate(r.RepoID, commit, r.Path); e != nil {
			return Diff{}, e
		}
		if _, e := s.blob(ctx, r.RepoID, commit, r.Path); e != nil {
			return Diff{}, e
		}
	}
	b, e := s.git(ctx, r.RepoID, s.config.MaxOutputBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3", r.LeftCommit, r.RightCommit, "--", r.Path)
	if e != nil {
		return Diff{}, e
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return Diff{}, ErrInvalidRequest
	}
	return Diff{RepoID: r.RepoID, LeftCommit: r.LeftCommit, RightCommit: r.RightCommit, Path: r.Path, Content: string(b), Hash: digest(b)}, nil
}
