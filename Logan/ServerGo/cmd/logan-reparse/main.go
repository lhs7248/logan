// logan-reparse is restricted to explicitly isolated integration databases.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/sys/unix"
	"logan/servergo/internal/config"
	"logan/servergo/internal/reparse"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
)

const maxPlanBytes = 64 << 10

var isolatedName = regexp.MustCompile(`^logan_reparse_it_[a-z0-9]+$`)
var operationName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }
func run(args []string, get func(string) string, out, errout io.Writer) int {
	if err := execute(args, get, out); err != nil {
		fmt.Fprintln(errout, "logan-reparse: operation rejected or failed; verify isolated configuration, arguments, private plan and database state")
		return 1
	}
	return 0
}
func execute(args []string, get func(string) string, out io.Writer) error {
	fs := flag.NewFlagSet("logan-reparse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	action := fs.String("action", "dry-run", "dry-run, apply, rollback, identity-export, identity-dry-run, or identity-migrate")
	taskID := fs.Int64("task-id", 0, "explicit positive Native task ID")
	planPath := fs.String("plan", "", "absolute private plan JSON path")
	operationID := fs.String("operation-id", "", "operation to roll back")
	writes := fs.Bool("allow-isolated-writes", false, "allow writes to isolated integration database")
	offline := fs.Bool("offline", false, "attest all other database writers and maintenance are stopped")
	witnessPath := fs.String("witness", "", "absolute private identity witness JSON path")
	witnessDigest := fs.String("witness-sha256", "", "trusted source witness digest")
	expectSource := fs.String("expect-source", "", "expected source database identity")
	expectTarget := fs.String("expect-target", "", "expected target database identity")
	migration := fs.Bool("allow-identity-migration", false, "allow one operation identity authorization")
	fenced := fs.Bool("source-fenced", false, "attest source database is fenced against writes")
	timeout := fs.Duration("timeout", 2*time.Minute, "total operation deadline")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *timeout <= 0 || *timeout > time.Hour {
		return errors.New("arguments")
	}
	if get("LOGAN_REPARSE_ISOLATED") != "YES" {
		return errors.New("isolation")
	}
	dsn, err := mysql.ParseDSN(get("LOGAN_MYSQL_DSN"))
	if err != nil || !isolatedName.MatchString(dsn.DBName) {
		return errors.New("database isolation")
	}
	idFlags := identityFlags{action: *action, witness: *witnessPath, digest: *witnessDigest, source: *expectSource, target: *expectTarget, offline: *offline, writes: *writes, migration: *migration, fenced: *fenced}
	if err := idFlags.validate(); err != nil {
		return err
	}
	identity := strings.HasPrefix(*action, "identity-")
	if !identity && *action != "dry-run" && *action != "apply" && *action != "rollback" {
		return errors.New("action")
	}
	if !identity && *action != "dry-run" && (!*writes || !*offline) {
		return errors.New("write guard")
	}
	if identity {
		if *operationID != "" {
			return errors.New("identity operation argument")
		}
		if *action == "identity-export" {
			if *taskID <= 0 || !filepath.IsAbs(*planPath) || *planPath == *witnessPath {
				return errors.New("identity export arguments")
			}
		} else if *taskID != 0 || *planPath != "" {
			return errors.New("identity migration arguments")
		}
	} else if *action == "rollback" {
		if !operationName.MatchString(*operationID) || *taskID != 0 || *planPath != "" {
			return errors.New("rollback arguments")
		}
	} else if *taskID <= 0 || *operationID != "" || !filepath.IsAbs(*planPath) {
		return errors.New("plan arguments")
	}
	c, err := loadConfig(*action, get)
	if err != nil {
		return errors.New("configuration")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var plan reparse.Plan
	if *action == "apply" || *action == "identity-export" {
		if err := readPlan(*planPath, &plan); err != nil {
			return err
		}
		if plan.TaskID != *taskID {
			return errors.New("task mismatch")
		}
	}
	var witness reparse.IdentityWitness
	if *action == "identity-dry-run" || *action == "identity-migrate" {
		if err := readPlan(*witnessPath, &witness); err != nil {
			return err
		}
	}
	// Reserve dry-run path before any connection; never overwrite an earlier plan.
	var reserved *os.File
	reservePath := *planPath
	if *action == "identity-export" {
		reservePath = *witnessPath
	}
	if *action == "dry-run" || *action == "identity-export" {
		reserved, err = createPlan(reservePath)
		if err != nil {
			return err
		}
		defer reserved.Close()
		defer func() {
			if reserved != nil {
				os.Remove(reservePath)
			}
		}()
	}
	db, pool, err := repository.Open(ctx, c.Database)
	if err != nil {
		return errors.New("database")
	}
	defer pool.Close()
	var objects reparse.ObjectReader
	if !identity && *action != "rollback" {
		s, e := storage.NewOSS(c.OSS)
		if e != nil {
			return errors.New("storage")
		}
		defer s.Close()
		objects = s
	}
	tool, err := reparse.New(db, objects, reparse.Limits{MaxCompressed: c.Native.MaxInput, MaxPlain: c.NativeQuery.MaxPlain, MaxLine: c.NativeQuery.MaxLine, MaxRecords: c.NativeQuery.MaxRecords, TempDir: c.Native.TempDir})
	if err != nil {
		return errors.New("tool")
	}
	if identity {
		if *action == "identity-export" {
			witness, err := tool.ExportIdentity(ctx, plan)
			if err != nil {
				return errors.New("identity export")
			}
			if err := encodeWitness(reserved, witness); err != nil {
				return errors.New("witness write")
			}
			if err := reserved.Sync(); err != nil {
				return errors.New("witness sync")
			}
			if err := reserved.Close(); err != nil {
				return errors.New("witness close")
			}
			reserved = nil
			return json.NewEncoder(out).Encode(struct {
				OperationID    string `json:"operation_id"`
				SourceIdentity string `json:"source_identity"`
				Digest         string `json:"digest"`
			}{plan.OperationID, witness.SourceIdentity, witness.Digest})
		}
		result, err := tool.MigrateIdentity(ctx, witness, *witnessDigest, *expectSource, *expectTarget, *action == "identity-dry-run")
		if err != nil {
			return errors.New("identity migration")
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *action == "dry-run" {
		plan, err = tool.DryRun(ctx, *taskID)
		if err != nil {
			return errors.New("dry run")
		}
		if err = json.NewEncoder(reserved).Encode(plan); err != nil {
			return errors.New("plan write")
		}
		if err = reserved.Sync(); err != nil {
			return errors.New("plan sync")
		}
		if err = reserved.Close(); err != nil {
			return errors.New("plan close")
		}
		reserved = nil
		return json.NewEncoder(out).Encode(struct {
			OperationID     string `json:"operation_id"`
			TaskID          int64  `json:"task_id"`
			DryRun          bool   `json:"dry_run"`
			ExistingRecords int    `json:"existing_records"`
			ParsedRecords   int    `json:"parsed_records"`
			AppendRecords   int    `json:"append_records"`
		}{plan.OperationID, plan.TaskID, true, plan.ExistingRecords, plan.ParsedRecords, plan.AppendRecords})
	}
	var result reparse.Result
	if *action == "apply" {
		result, err = tool.Apply(ctx, plan)
	} else {
		result, err = tool.Rollback(ctx, *operationID)
	}
	if err != nil {
		return errors.New("mutation")
	}
	// Explicit allowlist prevents future core metadata from leaking object names.
	return json.NewEncoder(out).Encode(struct {
		OperationID     string `json:"operation_id"`
		TaskID          int64  `json:"task_id"`
		DryRun          bool   `json:"dry_run"`
		State           string `json:"state"`
		AppendedRecords int    `json:"appended_records"`
		Noop            bool   `json:"noop"`
	}{result.OperationID, result.TaskID, false, result.State, result.AppendedRecords, result.Noop})
}
func createPlan(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, errors.New("plan path")
	}
	// O_NOFOLLOW protects final component, O_EXCL also refuses existing symlinks.
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("plan create")
	}
	return os.NewFile(uintptr(fd), path), nil
}
func writePlan(path string, v any) error {
	f, err := createPlan(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(v); err != nil {
		os.Remove(path)
		return err
	}
	return f.Sync()
}
func readPlan(path string, v any) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("plan path")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("plan open")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return errors.New("plan stat")
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || int(stat.Uid) != os.Geteuid() || st.Size() > maxPlanBytes {
		return errors.New("private plan required")
	}
	dec := json.NewDecoder(io.LimitReader(f, maxPlanBytes+1))
	dec.DisallowUnknownFields()
	if err = dec.Decode(v); err != nil {
		return errors.New("plan JSON")
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return errors.New("plan trailing data")
	}
	return nil
}

// loadConfig reuses server validation without requiring unused AES or OSS keys for rollback.
func loadConfig(action string, get func(string) string) (config.Config, error) {
	cfgGet := func(key string) string {
		switch key {
		case "LOGAN_DB_ENABLED":
			return "true"
		case "LOGAN_WEB_ENABLED", "LOGAN_MAINTENANCE_ENABLED":
			return "false"
		case "LOGAN_NATIVE_ENABLED", "LOGAN_NATIVE_QUERY_ENABLED":
			if action == "rollback" || strings.HasPrefix(action, "identity-") {
				return "false"
			}
			return "true"
		case "LOGAN_NATIVE_AES_KEY", "LOGAN_NATIVE_AES_IV":
			return "unused-reparse-0"
		}
		return get(key)
	}
	return config.Load(cfgGet)
}
