//go:build integration

package mysql

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"logan/servergo/internal/config"
	"logan/servergo/internal/maintenance"
	"logan/servergo/internal/repository"
)

//go:embed java_schema.sql
var javaSchema string
var testDSN string

func TestMain(m *testing.M) {
	cfg, err := driver.ParseDSN(os.Getenv("LOGAN_IT_MYSQL_DSN"))
	if err != nil || cfg == nil || !strings.HasPrefix(cfg.DBName, "logan_it_") || os.Getenv("LOGAN_IT_ALLOW_WRITES") != "1" {
		fmt.Fprintln(os.Stderr, "MySQL integration requires LOGAN_IT_MYSQL_DSN with a dedicated logan_it_ database and LOGAN_IT_ALLOW_WRITES=1 (DSN withheld)")
		os.Exit(1)
	}
	if child := os.Getenv("LOGAN_MYSQL_CHILD_DSN"); child != "" {
		childCfg, e := driver.ParseDSN(child)
		mode := os.Getenv("LOGAN_MYSQL_CHILD_MODE")
		allowedMode := mode == "claim" || mode == "recover" || mode == "native-claim" || mode == "native-recover"
		runOK := false
		for _, arg := range os.Args[1:] {
			if arg == "-test.run=^TestMySQLWorkerProcess$" {
				runOK = true
			}
		}
		if e != nil || !regexp.MustCompile(`^logan_it_[a-f0-9]{32}$`).MatchString(childCfg.DBName) || !allowedMode || !runOK {
			failSetup()
		}
		compare := childCfg.Clone()
		compare.DBName = cfg.DBName
		compare.ParseTime = cfg.ParseTime
		if !reflect.DeepEqual(compare, cfg) {
			failSetup()
		}
		testDSN = child
		os.Exit(m.Run())
	}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		failSetup()
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if control.PingContext(ctx) != nil {
		failSetup()
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		failSetup()
	}
	name := "logan_it_" + hex.EncodeToString(token[:])
	if _, err = control.ExecContext(ctx, "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		failSetup()
	}
	cfg.DBName = name
	cfg.ParseTime = true
	testDSN = cfg.FormatDSN()
	code := func() int {

		db, pool, e := openDB()
		if e != nil {
			fmt.Fprintln(os.Stderr, "isolated MySQL connection failed")
			return 1
		}
		defer pool.Close()
		var version string
		if db.Raw("SELECT VERSION()").Scan(&version).Error == nil {
			fmt.Println("MySQL integration server version: " + version)
		}
		scripts := []string{javaSchema}
		for _, n := range []string{"001_web_processing.sql", "002_maintenance.sql"} {
			b, e := os.ReadFile(filepath.Join("..", "..", "..", "migrations", n))
			if e != nil {
				return 1
			}
			scripts = append(scripts, string(b))
		}
		for _, script := range scripts {
			var lines []string
			for _, line := range strings.Split(script, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "--") {
					lines = append(lines, line)
				}
			}
			for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
				if strings.TrimSpace(stmt) != "" {
					if db.Exec(stmt).Error != nil {
						fmt.Fprintln(os.Stderr, "schema setup failed (details withheld)")
						return 1
					}
				}
			}
		}
		return m.Run()
	}()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, cleanupErr := control.ExecContext(cleanupCtx, "DROP DATABASE `"+name+"`")
	cleanupCancel()
	if cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "isolated MySQL cleanup failed (details withheld)")
		code = 1
	}
	os.Exit(code)
}
func failSetup() {
	fmt.Fprintln(os.Stderr, "MySQL integration setup failed; check dedicated test schema connectivity and CREATE DATABASE permission (details withheld)")
	os.Exit(1)
}
func openDB() (*gorm.DB, *sql.DB, error) {
	return repository.Open(context.Background(), config.Database{Enabled: true, DSN: testDSN, MaxOpen: 8, MaxIdle: 4, PingTimeout: 5 * time.Second})
}
func dbFor(t *testing.T) *gorm.DB {
	t.Helper()
	db, p, e := openDB()
	if e != nil {
		t.Fatal("isolated MySQL connection failed")
	}
	t.Cleanup(func() { p.Close() })
	return db
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func lease(t *testing.T, r *repository.Maintenance, name, owner string, ttl time.Duration) maintenance.Lease {
	t.Helper()
	l, ok, e := r.Acquire(context.Background(), name, owner, ttl)
	must(t, e)
	if !ok {
		t.Fatal("lease unavailable")
	}
	return l
}
func count(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	must(t, db.Table(table).Where(where, args...).Count(&n).Error)
	return n
}

func TestAtomicUploadRetentionAndDurableOutbox(t *testing.T) {
	db := dbFor(t)
	ctx := context.Background()
	r := repository.NewMaintenance(db, "native-it")
	must(t, r.Validate(ctx))
	name := "atomic-object"
	task := repository.NativeTask{Platform: 1, LogFileName: &name, AddTime: 1}
	tracked := repository.NewTrackedNativeTasks(db, "native-it")
	if tracked.Create(ctx, &task) == nil {
		t.Fatal("publication without intent succeeded")
	}
	must(t, r.BeginObject(ctx, "native-it", name))
	must(t, tracked.Create(ctx, &task))
	if tracked.Create(ctx, &repository.NativeTask{Platform: 1, LogFileName: &name, AddTime: 1}) == nil {
		t.Fatal("intent reused")
	}
	must(t, db.Create(&repository.NativeTask{Platform: 1, LogFileName: &name, AddTime: 9999999999999}).Error)
	must(t, db.Create(&repository.NativeDetail{TaskID: task.ID, LogType: 1, Content: "expired", LogTime: 1}).Error)
	l := lease(t, r, "retention", "owner", 10*time.Second)
	c, e := r.Prune(ctx, l, 2, 20, "native-it")
	must(t, e)
	if c.NativeTasks != 1 || c.NativeDetails != 1 {
		t.Fatalf("unexpected prune counts: %+v", c)
	}
	objects, e := r.Claim(ctx, l, "native-it", time.Second, 20)
	must(t, e)
	if len(objects) != 0 {
		t.Fatal("shared referenced object claimed for deletion")
	}
	must(t, db.Where("log_file_name = ?", name).Delete(&repository.NativeTask{}).Error)
	_, e = r.Sweep(ctx, l, "native-it", time.Now().Add(time.Second).UnixMilli(), 20)
	must(t, e)
	// Reopen a pool: outbox state must survive repository/pool replacement.
	db2 := dbFor(t)
	r2 := repository.NewMaintenance(db2, "native-it")
	objects, e = r2.Claim(ctx, l, "native-it", time.Second, 20)
	must(t, e)
	if len(objects) != 1 {
		t.Fatalf("durable outbox claims=%d", len(objects))
	}
	must(t, r2.Finish(ctx, l, objects[0], "", 0))
	if r.BeginObject(ctx, "native-it", name) == nil {
		t.Fatal("deleted tombstone allowed reuse")
	}
}

type childClaim struct {
	Lease  maintenance.Lease
	Object maintenance.Object
}

func TestMySQLWorkerProcess(t *testing.T) {
	mode := os.Getenv("LOGAN_MYSQL_CHILD_MODE")
	if mode == "" {
		return
	}
	db := dbFor(t)
	r := repository.NewMaintenance(db, "native-it")
	ctx := context.Background()
	if strings.HasPrefix(mode, "native-") {
		id, e := strconv.ParseInt(os.Getenv("LOGAN_MYSQL_CHILD_TASK"), 10, 64)
		must(t, e)
		q := repository.NewNativeQueries(db, 1<<20)
		if mode == "native-claim" {
			must(t, q.Process(ctx, id, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
				fmt.Println("NATIVE_CLAIMED")
				select {}
			}))
			return
		}
		deadline := time.Now().Add(40 * time.Second)
		for time.Now().Before(deadline) {
			ids, e := q.Pending(ctx, 100)
			must(t, e)
			for _, pending := range ids {
				if pending == id {
					must(t, q.Process(ctx, id, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
						return io.NopCloser(strings.NewReader(`{"Content":"recovered","LogType":1,"LogTime":1}`)), nil
					}))
					fmt.Println("NATIVE_RECOVERED")
					return
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("native recovery did not reach production lease deadline")
	}
	if mode == "claim" {
		l, ok, e := r.Acquire(ctx, "crash-recovery", "child", 400*time.Millisecond)
		must(t, e)
		if !ok {
			t.Fatal("child lease unavailable")
		}
		o, e := r.Claim(ctx, l, "native-it", 400*time.Millisecond, 1)
		must(t, e)
		if len(o) != 1 {
			t.Fatal("child claim unavailable")
		}
		b, _ := json.Marshal(childClaim{l, o[0]})
		fmt.Println("CLAIM " + string(b))
		select {}
	}
	if mode == "recover" {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			l, ok, e := r.Acquire(ctx, "crash-recovery", "replacement", 3*time.Second)
			must(t, e)
			if ok {
				o, e := r.Claim(ctx, l, "native-it", 3*time.Second, 1)
				must(t, e)
				if len(o) == 1 {
					must(t, r.Finish(ctx, l, o[0], "", 0))
					fmt.Println("RECOVERED")
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("replacement could not reclaim after DB lease deadline")
	}
}
func child(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMySQLWorkerProcess$", "-test.timeout=50s")
	cmd.Env = append(os.Environ(), "LOGAN_MYSQL_CHILD_DSN="+testDSN, "LOGAN_MYSQL_CHILD_MODE="+mode)
	return cmd
}
func TestKilledProcessRecoveryAndFencing(t *testing.T) {
	db := dbFor(t)
	ctx := context.Background()
	r := repository.NewMaintenance(db, "native-it")
	must(t, r.BeginObject(ctx, "native-it", "crash-object"))
	l := lease(t, r, "crash-setup", "setup", 5*time.Second)
	_, e := r.Sweep(ctx, l, "native-it", time.Now().Add(time.Second).UnixMilli(), 100)
	must(t, e)
	cmd := child(t, "claim")
	out, e := cmd.StdoutPipe()
	must(t, e)
	must(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	ready := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(out)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "CLAIM ") {
				ready <- strings.TrimPrefix(scan.Text(), "CLAIM ")
				return
			}
		}
		ready <- ""
	}()
	var old childClaim
	select {
	case line := <-ready:
		must(t, json.Unmarshal([]byte(line), &old))
	case <-time.After(8 * time.Second):
		t.Fatal("claim barrier timed out")
	}
	must(t, cmd.Process.Kill())
	if cmd.Wait() == nil {
		t.Fatal("SIGKILL unexpectedly succeeded")
	}
	recovery := child(t, "recover")
	b, e := recovery.CombinedOutput()
	if e != nil {
		t.Fatalf("replacement process failed: %s", b)
	}
	if !strings.Contains(string(b), "RECOVERED") {
		t.Fatal("missing replacement completion barrier")
	}
	if r.Finish(ctx, old.Lease, old.Object, "", 0) == nil {
		t.Fatal("stale process ownership accepted")
	}
	if count(t, db, "logan_managed_object", "name = ?", "crash-object") != 0 {
		t.Fatal("recovered tombstone still contains object key")
	}
}

func TestNativePublicationRollbackAndRetry(t *testing.T) {
	db := dbFor(t)
	ctx := context.Background()
	name := "parse-object"
	task := repository.NativeTask{Platform: 1, LogFileName: &name, AddTime: time.Now().UnixMilli()}
	must(t, db.Create(&task).Error)
	q := repository.NewNativeQueries(db, 1<<20)
	must(t, q.Ensure(ctx, task.ID))
	e := q.Process(ctx, task.ID, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"Content":"first","LogType":1,"LogTime":1}` + "\n{")), nil
	})
	if e == nil {
		t.Fatal("malformed staging stream committed")
	}
	if count(t, db, "logan_log_detail", "task_id = ?", task.ID) != 0 {
		t.Fatal("partial detail publication")
	}
	var status int
	must(t, db.Raw("SELECT status FROM logan_task WHERE id=?", task.ID).Scan(&status).Error)
	if status != 0 {
		t.Fatal("failed publication marked task done")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ids, e := q.Pending(ctx, 100)
		must(t, e)
		found := false
		for _, id := range ids {
			if id == task.ID {
				found = true
			}
		}
		if found {
			must(t, q.Process(ctx, task.ID, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(`{"Content":"complete","LogType":1,"LogTime":1}`)), nil
			}))
			ready, e := q.Ready(ctx, task.ID)
			must(t, e)
			if !ready {
				t.Fatal("retry not ready")
			}
			if count(t, db, "logan_log_detail", "task_id = ?", task.ID) != 1 {
				t.Fatal("retry detail count")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("retry deadline exceeded")
}

func TestCompetingLeaseAndExpiredOwner(t *testing.T) {
	db := dbFor(t)
	r := repository.NewMaintenance(db, "native-it")
	ctx := context.Background()
	start := make(chan struct{})
	type result struct {
		l  maintenance.Lease
		ok bool
		e  error
	}
	results := make(chan result, 2)
	for _, owner := range []string{"competitor-a", "competitor-b"} {
		go func(owner string) {
			<-start
			l, ok, e := r.Acquire(ctx, "competition", owner, 300*time.Millisecond)
			results <- result{l, ok, e}
		}(owner)
	}
	close(start)
	wins := 0
	var old maintenance.Lease
	for i := 0; i < 2; i++ {
		v := <-results
		must(t, v.e)
		if v.ok {
			wins++
			old = v.l
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent winners=%d", wins)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		next, ok, e := r.Acquire(ctx, "competition", "new-owner", time.Second)
		must(t, e)
		if ok {
			if next.Fence <= old.Fence {
				t.Fatal("fence did not advance")
			}
			if r.Renew(ctx, old, time.Second) == nil || r.Release(ctx, old) == nil {
				t.Fatal("expired owner accepted")
			}
			must(t, r.Release(ctx, next))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lease never expired")
}

func TestKilledNativeParserProcessRecovery(t *testing.T) {
	db := dbFor(t)
	ctx := context.Background()
	name := "killed-parser-object"
	task := repository.NativeTask{Platform: 1, LogFileName: &name, AddTime: time.Now().UnixMilli()}
	must(t, db.Create(&task).Error)
	q := repository.NewNativeQueries(db, 1<<20)
	must(t, q.Ensure(ctx, task.ID))
	cmd := child(t, "native-claim")
	cmd.Env = append(cmd.Env, "LOGAN_MYSQL_CHILD_TASK="+strconv.FormatInt(task.ID, 10))
	out, e := cmd.StdoutPipe()
	must(t, e)
	must(t, cmd.Start())
	t.Cleanup(func() { cmd.Process.Kill() })
	barrier := make(chan bool, 1)
	go func() {
		scan := bufio.NewScanner(out)
		for scan.Scan() {
			if scan.Text() == "NATIVE_CLAIMED" {
				barrier <- true
				return
			}
		}
		barrier <- false
	}()
	select {
	case ready := <-barrier:
		if !ready {
			t.Fatal("parser child exited before claim")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("parser claim barrier timed out")
	}
	var before struct {
		State, Owner                string
		Fence, LeaseUntil, Attempts int64
	}
	must(t, db.Raw("SELECT state,owner,fence,lease_until,attempts FROM logan_processing_job WHERE kind='native' AND task_id=?", task.ID).Scan(&before).Error)
	if before.State != "running" || before.Owner == "" || before.Fence != 1 || before.Attempts != 1 {
		t.Fatalf("unexpected claimed state: state=%s fence=%d attempts=%d", before.State, before.Fence, before.Attempts)
	}
	if count(t, db, "logan_log_detail", "task_id = ?", task.ID) != 0 {
		t.Fatal("blocked parser published details")
	}
	must(t, cmd.Process.Kill())
	if cmd.Wait() == nil {
		t.Fatal("parser was not killed")
	}
	replacement := child(t, "native-recover")
	replacement.Env = append(replacement.Env, "LOGAN_MYSQL_CHILD_TASK="+strconv.FormatInt(task.ID, 10))
	output, e := replacement.CombinedOutput()
	if e != nil {
		t.Fatalf("replacement parser failed: %s", output)
	}
	if !strings.Contains(string(output), "NATIVE_RECOVERED") {
		t.Fatal("missing recovery barrier")
	}
	ready, e := q.Ready(ctx, task.ID)
	must(t, e)
	if !ready {
		t.Fatal("replacement task not ready")
	}
	var details []repository.NativeDetail
	must(t, db.Where("task_id = ?", task.ID).Find(&details).Error)
	if len(details) != 1 || details[0].Content != "recovered" || details[0].TaskID != task.ID {
		t.Fatal("replacement publication not exactly once")
	}
	stableID := details[0].ID
	must(t, q.Process(ctx, task.ID, func(context.Context, repository.NativeTask) (io.ReadCloser, error) {
		t.Fatal("done job parsed again")
		return nil, nil
	}))
	var after struct {
		State, Owner    string
		Fence, Attempts int64
	}
	must(t, db.Raw("SELECT state,owner,fence,attempts FROM logan_processing_job WHERE kind='native' AND task_id=?", task.ID).Scan(&after).Error)
	if after.State != "done" || after.Owner != "" || after.Fence != before.Fence+1 || after.Attempts != 2 {
		t.Fatalf("unexpected recovered state: state=%s fence=%d attempts=%d", after.State, after.Fence, after.Attempts)
	}
	if count(t, db, "logan_log_detail", "task_id = ? AND id = ?", task.ID, stableID) != 1 {
		t.Fatal("detail ID changed after repeat delivery")
	}
	t.Logf("native crash recovery: task=%d running fence=%d attempts=%d -> done fence=%d attempts=%d; detail=%d", task.ID, before.Fence, before.Attempts, after.Fence, after.Attempts, stableID)
}

func TestWebPublicationAndRetention(t *testing.T) {
	db := dbFor(t)
	ctx := context.Background()
	web := repository.NewWebRepository(db, 1<<20)
	must(t, web.Validate(ctx))
	task := repository.WebTask{DeviceID: "web-integration-device", PageNum: 17, Content: "synthetic staged source", AddTime: 3, LogDate: 123456789, Status: 0}
	must(t, web.Save(ctx, &task))
	must(t, web.Ensure(ctx, task.ID))
	must(t, web.Process(ctx, task.ID, func(_ context.Context, source repository.WebTask) ([]repository.WebDetail, error) {
		if source.ID != task.ID || source.Content != task.Content {
			t.Fatal("parser source changed")
		}
		var status int
		must(t, db.Raw("SELECT status FROM web_task WHERE id=?", task.ID).Scan(&status).Error)
		if status != 0 || count(t, db, "web_detail", "task_id = ?", task.ID) != 0 {
			t.Fatal("publication visible before parser completed")
		}
		return []repository.WebDetail{{Content: "web record one", LogType: 1, LogTime: 123456790, AddTime: 4, MinuteOffset: 1}, {Content: "web record two", LogType: 2, LogTime: 123456791, AddTime: 4, MinuteOffset: 2}}, nil
	}))
	ready, e := web.Ready(ctx, []int64{task.ID})
	must(t, e)
	if !ready {
		t.Fatal("web publication not ready")
	}
	var original []repository.WebDetail
	must(t, db.Where("task_id = ?", task.ID).Order("id").Find(&original).Error)
	if len(original) != 2 {
		t.Fatal("web publication detail count")
	}
	ids := []int64{original[0].ID, original[1].ID}
	details, e := web.Details(ctx, ids)
	must(t, e)
	if len(details) != 2 || details[0].Content != "web record one" || details[1].Content != "web record two" {
		t.Fatal("web detail query mismatch")
	}
	must(t, web.Process(ctx, task.ID, func(context.Context, repository.WebTask) ([]repository.WebDetail, error) {
		t.Fatal("completed web task parsed twice")
		return nil, nil
	}))
	overwrite := repository.WebTask{DeviceID: task.DeviceID, PageNum: task.PageNum, LogDate: task.LogDate, Content: "legacy completed-page overwrite", AddTime: 9999999999999}
	must(t, web.Save(ctx, &overwrite))
	if overwrite.ID != task.ID || overwrite.Status != 1 {
		t.Fatal("completed page identity/status changed")
	}
	must(t, web.Ensure(ctx, task.ID))
	must(t, web.Process(ctx, task.ID, func(context.Context, repository.WebTask) ([]repository.WebDetail, error) {
		t.Fatal("completed page overwrite reparsed")
		return nil, nil
	}))
	var after []repository.WebDetail
	must(t, db.Where("task_id = ?", task.ID).Order("id").Find(&after).Error)
	if !reflect.DeepEqual(original, after) {
		t.Fatal("legacy overwrite changed existing details/IDs")
	}
	var source repository.WebTask
	must(t, db.First(&source, task.ID).Error)
	if source.Content != overwrite.Content {
		t.Fatal("legacy overwrite did not update source content")
	}
	maintenanceRepo := repository.NewMaintenance(db, "native-it")
	l := lease(t, maintenanceRepo, "web-retention", "web-retention-owner", 10*time.Second)
	pruned, e := maintenanceRepo.Prune(ctx, l, 4, 100, "native-it")
	must(t, e)
	if pruned.WebTasks != 1 || pruned.WebDetails != 2 {
		t.Fatalf("web prune counts: %+v", pruned)
	}
	if count(t, db, "web_task", "id = ?", task.ID) != 0 || count(t, db, "web_detail", "task_id = ?", task.ID) != 0 || count(t, db, "logan_processing_job", "kind = 'web' AND task_id = ?", task.ID) != 0 {
		t.Fatal("web retention left task/details/job")
	}
}
