package reparse

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"logan/servergo/internal/protocol"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrefixRejectsMismatchAndNull(t *testing.T) {
	s := "x"
	expected := []protocol.NativeRecord{{Content: &s, LogType: 1, LogTime: 2}}
	if err := prefix([]map[string]any{{"content": "x", "log_type": "1", "log_time": "2"}}, expected); err != nil {
		t.Fatal(err)
	}
	if err := prefix([]map[string]any{{"content": "wrong", "log_type": "1", "log_time": "2"}}, expected); err == nil {
		t.Fatal("accepted divergent prefix")
	}
	expected[0].Content = nil
	if err := prefix([]map[string]any{{"content": "", "log_type": "1", "log_time": "2"}}, expected); err == nil {
		t.Fatal("null content cannot be stored losslessly")
	}
}
func TestFingerprintBindsEveryPlanField(t *testing.T) {
	p := Plan{OperationID: "a", TaskID: 1}
	p.Fingerprint = fingerprint(p)
	changed := p
	changed.AppendRecords++
	if fingerprint(changed) == p.Fingerprint {
		t.Fatal("counter tampering undetected")
	}
}

func TestNewAllowsRollbackWithoutOSS(t *testing.T) {
	db, mock := testDB(t)
	_ = mock
	if _, err := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1}); err != nil {
		t.Fatal(err)
	}
}
func TestDryRunNeverWritesAndBindsObjectAndEveryTaskField(t *testing.T) {
	db, mock := testDB(t)
	expectEngines(mock, false)
	mock.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("uuid", "isolated"))
	for i := 0; i < 2; i++ {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM logan_processing_job").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("done"))
		mock.ExpectQuery("SELECT \\* FROM logan_task").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "log_file_name", "extra_metadata"}).AddRow(7, 1, "object", "preserve"))
		mock.ExpectQuery("SELECT \\* FROM logan_log_detail").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "content", "log_type", "log_time", "add_time"}).AddRow(33, 7, "first", 1, 2, "old"))
		mock.ExpectCommit()
	}
	tool, _ := New(db, objectBody{gzipBytes(t, "{\"c\":\"first\",\"f\":1,\"l\":2}\n{\"c\":\"tail\",\"f\":2,\"l\":3}\n")}, Limits{MaxCompressed: 4096, MaxPlain: 4096, MaxLine: 1024, MaxRecords: 10, TempDir: t.TempDir()})
	p, err := tool.DryRun(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExistingRecords != 1 || p.AppendRecords != 1 || p.ParsedRecords != 2 || len(p.OperationID) != 32 || p.Fingerprint != fingerprint(p) {
		t.Fatalf("bad plan: %+v", p)
	}
	raw, _ := json.Marshal(p)
	if bytes.Contains(raw, []byte("first")) || bytes.Contains(raw, []byte("preserve")) {
		t.Fatal("raw log or metadata leaked into plan")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestDryRunRejectsNonInnoDBBeforeReadingOSS(t *testing.T) {
	db, mock := testDB(t)
	mock.ExpectQuery("SELECT ENGINE").WithArgs("logan_task").WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}).AddRow("MyISAM"))
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1})
	if _, err := tool.DryRun(context.Background(), 7); err == nil {
		t.Fatal("accepted nontransactional table")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func testDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	pool, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close() })
	db, e := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	return db, m
}
func expectEngines(m sqlmock.Sqlmock, audit bool) {
	tables := []string{"logan_task", "logan_log_detail", "logan_processing_job"}
	if audit {
		tables = append(tables, auditTable)
	}
	for _, table := range tables {
		m.ExpectQuery("SELECT ENGINE").WithArgs(table).WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}).AddRow("InnoDB"))
	}
}

type objectBody struct{ body []byte }

func (o objectBody) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(o.body)), nil
}
func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, e := w.Write([]byte(s)); e != nil {
		t.Fatal(e)
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestParseCancellationClosesBlockedObject(t *testing.T) {
	db, _ := testDB(t)
	body := &blockedBody{closed: make(chan struct{})}
	tool, _ := New(db, blockedObject{body}, Limits{MaxCompressed: 4096, MaxPlain: 4096, MaxLine: 1024, MaxRecords: 10, TempDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, _, _, e := tool.parse(ctx, "object"); done <- e }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancel accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("object download ignored cancellation")
	}
}

type blockedBody struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockedBody) Read([]byte) (int, error) { <-b.closed; return 0, context.Canceled }
func (b *blockedBody) Close() error             { b.once.Do(func() { close(b.closed) }); return nil }

type blockedObject struct{ b *blockedBody }

func (b blockedObject) Get(context.Context, string) (io.ReadCloser, error) { return b.b, nil }

func TestRollbackRefusesChangedSnapshotBeforeDeleting(t *testing.T) {
	db, m := testDB(t)
	expectEngines(m, true)
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("uuid", "isolated"))
	m.ExpectBegin()
	m.ExpectQuery("SELECT operation_id,task_id").WithArgs("operation").WillReturnRows(sqlmock.NewRows([]string{"operation_id", "task_id", "database_identity", "state", "after_hash", "append_ids"}).AddRow("operation", 7, "uuid/isolated", "applied", "different", "[34]"))
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("uuid", "isolated"))
	m.ExpectQuery("SELECT \\* FROM logan_processing_job").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("done"))
	m.ExpectQuery("SELECT \\* FROM logan_task").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(7, 1))
	m.ExpectQuery("SELECT \\* FROM logan_log_detail").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "content"}).AddRow(34, "changed"))
	m.ExpectRollback()
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1})
	if _, e := tool.Rollback(context.Background(), "operation"); e == nil {
		t.Fatal("changed rows allowed rollback")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestRejectsOverflowingLimits(t *testing.T) {
	db, _ := testDB(t)
	for _, l := range []Limits{{MaxCompressed: math.MaxInt64, MaxPlain: 1, MaxLine: 1, MaxRecords: 1}, {MaxCompressed: 1, MaxPlain: math.MaxInt64, MaxLine: 1, MaxRecords: 1}} {
		if _, e := New(db, nil, l); e == nil {
			t.Fatal("accepted overflowing resource budget")
		}
	}
}
func TestSnapshotBoundsCountAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int
		budget    int64
		wantError bool
	}{{"count", 1, 1024, true}, {"bytes", 3, 8, true}, {"fits", 3, 1024, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, m := testDB(t)
			m.ExpectQuery("SELECT test").WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("first").AddRow("second"))
			_, e := boundedRows(db, tc.count, tc.budget, "SELECT test")
			if (e != nil) != tc.wantError {
				t.Fatalf("error=%v", e)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestPublicationRejectsCoercionAndTriggerMutation(t *testing.T) {
	s := "original"
	tail := "tail"
	r := []protocol.NativeRecord{{Content: &s, LogType: 1, LogTime: 1}, {Content: &tail, LogType: 2, LogTime: -1}}
	before := snapshot{Task: map[string]any{"status": 1, "metadata": "keep"}, Details: []map[string]any{{"id": 10, "content": "original", "log_type": 1, "log_time": 1, "update_time": "old"}}}
	after := snapshot{Task: map[string]any{"status": 1, "metadata": "keep"}, Details: []map[string]any{{"id": 10, "content": "original", "log_type": 1, "log_time": 1, "update_time": "old"}, {"id": 11, "content": "tail", "log_type": 2, "log_time": -1}}}
	if e := published(before, after, r); e != nil {
		t.Fatal(e)
	}
	after.Details[1]["log_time"] = 0
	if e := published(before, after, r); e == nil {
		t.Fatal("accepted MySQL unsigned coercion")
	}
	after.Details[1]["log_time"] = -1
	after.Details[1]["content"] = "ta"
	if e := published(before, after, r); e == nil {
		t.Fatal("accepted truncated content")
	}
	after.Details[1]["content"] = "tail"
	after.Details[0]["update_time"] = "triggered"
	if e := published(before, after, r); e == nil {
		t.Fatal("accepted old-row trigger mutation")
	}
	after.Details[0]["update_time"] = "old"
	after.Task["metadata"] = "triggered"
	if e := published(before, after, r); e == nil {
		t.Fatal("accepted task trigger mutation")
	}
}

func TestApplyRollsBackCoercedPublication(t *testing.T) {
	db, m := testDB(t)
	body := gzipBytes(t, "{\"c\":\"first\",\"f\":1,\"l\":2}\n{\"c\":\"tail\",\"f\":2,\"l\":-1}\n")
	tool, _ := New(db, objectBody{body}, Limits{MaxCompressed: 4096, MaxPlain: 4096, MaxLine: 1024, MaxRecords: 10, TempDir: t.TempDir()})
	r, d, c, n, e := tool.parse(context.Background(), "object")
	if e != nil {
		t.Fatal(e)
	}
	before := snapshot{Task: map[string]any{"id": int64(7), "status": int64(1), "log_file_name": "object"}, Details: []map[string]any{{"id": int64(33), "task_id": int64(7), "content": "first", "log_type": int64(1), "log_time": int64(2)}}}
	p := Plan{OperationID: strings.Repeat("a", 32), TaskID: 7, ObjectName: "object", ObjectSHA256: d, ExpectedDigest: hash(r), BeforeHash: hash(before), DatabaseIdentity: "uuid/isolated", SchemaVersion: SchemaVersion, ToolVersion: ToolVersion, ExistingRecords: 1, ParsedRecords: 2, AppendRecords: 1, CompressedBytes: c, PlainBytes: n}
	p.Fingerprint = fingerprint(p)
	expectEngines(m, true)
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("uuid", "isolated"))
	m.ExpectQuery("SELECT `operation_id`").WillReturnRows(sqlmock.NewRows([]string{"operation_id"}))
	m.ExpectBegin()
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("uuid", "isolated"))
	m.ExpectExec("INSERT INTO `logan_native_reparse_operation`").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT \\* FROM logan_processing_job").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("done"))
	for i := 0; i < 2; i++ {
		m.ExpectQuery("SELECT \\* FROM logan_task").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "log_file_name"}).AddRow(7, 1, "object"))
		details := sqlmock.NewRows([]string{"id", "task_id", "content", "log_type", "log_time"}).AddRow(33, 7, "first", 1, 2)
		if i == 1 {
			details.AddRow(34, 7, "tail", 2, 0)
		}
		m.ExpectQuery("SELECT \\* FROM logan_log_detail").WithArgs(int64(7)).WillReturnRows(details)
		if i == 0 {
			m.ExpectExec("INSERT INTO logan_log_detail").WithArgs(int64(7), 2, "tail", int64(-1)).WillReturnResult(sqlmock.NewResult(34, 1))
			m.ExpectQuery("SELECT LAST_INSERT_ID").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(34))
		}
	}
	m.ExpectRollback()
	m.ExpectQuery("SELECT `operation_id`").WillReturnRows(sqlmock.NewRows([]string{"operation_id"}))
	if _, e = tool.Apply(context.Background(), p); e == nil {
		t.Fatal("coerced publication succeeded")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestAppendIDsMustMatchActualSuffix(t *testing.T) {
	after := snapshot{Details: []map[string]any{{"id": int64(10)}, {"id": int64(15)}, {"id": int64(18)}}}
	if e := appendIDsMatch(after, 1, []int64{15, 18}); e != nil {
		t.Fatal(e)
	}
	for _, ids := range [][]int64{{0, 18}, {15, 15}, {14, 18}, {18, 15}, {15}, {15, 18, 20}} {
		if e := appendIDsMatch(after, 1, ids); e == nil {
			t.Fatalf("accepted bad audit IDs %v", ids)
		}
	}
}

func TestDetailSnapshotHasSQLRowLimitBeforeLock(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 1024, MaxPlain: 1024, MaxLine: 1024, MaxRecords: 2})
	m.ExpectQuery("SELECT \\* FROM logan_task WHERE id=\\? FOR UPDATE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	m.ExpectQuery("SELECT \\* FROM logan_log_detail WHERE task_id=\\? ORDER BY id LIMIT 3 FOR UPDATE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, e := tool.load(db, 7, true); e != nil {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
