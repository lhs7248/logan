package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"io"
	"strings"
	"testing"
	"time"
)

func nativeMock(t *testing.T) (*NativeQueries, sqlmock.Sqlmock) {
	r, m := webMock(t)
	return NewNativeQueries(r.db, 32<<20), m
}
func nativeStream(v []NativeDetail) io.ReadCloser {
	var b bytes.Buffer
	for _, d := range v {
		if e := json.NewEncoder(&b).Encode(d); e != nil {
			panic(e)
		}
	}
	return io.NopCloser(&b)
}
func nativeSourceRows(status int, content string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "status", "log_file_name"}).AddRow(8, status, content)
}
func TestNativeClaimCASLossDoesNotParse(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectExec("UPDATE logan_processing_job SET state='running'").WillReturnResult(sqlmock.NewResult(0, 0))
	called := false
	e := r.Process(context.Background(), 8, func(context.Context, NativeTask) (io.ReadCloser, error) { called = true; return nil, nil })
	if e != nil || called {
		t.Fatal(e, called)
	}
}
func TestNativeEnsureAlreadyAnalyzed(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO logan_processing_job.*ON DUPLICATE KEY UPDATE task_id=task_id").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"task_id", "state"}).AddRow(4, "pending"))
	m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status", "log_file_name"}).AddRow(4, 1, "overwritten"))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.Ensure(context.Background(), 4); e != nil {
		t.Fatal(e)
	}
}
func TestNativeSnapshotFenceLost(t *testing.T) {
	r, m := nativeMock(t)
	var owner string
	expectClaim(m, &owner)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "another-owner", digestWeb("a"), 2, 1))
	m.ExpectRollback()
	expectFail(m)
	called := false
	e := r.Process(context.Background(), 8, func(context.Context, NativeTask) (io.ReadCloser, error) { called = true; return nil, nil })
	if !errors.Is(e, errWebLease) || called {
		t.Fatal(e, called)
	}
}

func TestNativePendingExpiredRecovery(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectQuery("SELECT task_id,owner,fence,lease_until FROM logan_processing_job").WithArgs("native", 3, 20).WillReturnRows(sqlmock.NewRows([]string{"task_id", "owner", "fence", "lease_until"}).AddRow(7, "old", 4, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='failed'.*WHERE kind=.*AND task_id=.*AND state='running'.*AND owner=.*AND fence=.*AND lease_until=.*AND lease_until<=.*AND attempts>=").WithArgs("native", int64(7), "old", int64(4), int64(1), 3).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT task_id.*state='running' AND lease_until<=").WithArgs(3, 20).WillReturnRows(sqlmock.NewRows([]string{"task_id"}).AddRow(8))
	v, e := r.Pending(context.Background(), 20)
	if e != nil || len(v) != 1 || v[0] != 8 {
		t.Fatal(v, e)
	}
}

func expectNativeSnapshot(m sqlmock.Sqlmock, content string) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "owner", digestWeb(content), 3, 2))
	m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(nativeSourceRows(0, content))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST.*lease_until>").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
}
func expectNativePublishLocks(m sqlmock.Sqlmock, owner, content string, fence, revision int64) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", owner, digestWeb("old"), fence, revision))
	if owner == "owner" && fence == 3 && revision == 2 {
		m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(nativeSourceRows(0, content))
	}
}
func TestNativeParseFailureRetry(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectFail(m)
	bad := errors.New("bad source")
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) { return nil, bad }); !errors.Is(e, bad) {
		t.Fatal(e)
	}
}
func TestNativePublishRejectsStaleSourceAndFence(t *testing.T) {
	for _, tc := range []struct {
		name, owner, content string
		fence, revision      int64
	}{{"source", "owner", "new", 3, 2}, {"fence", "owner", "old", 4, 2}, {"owner", "other", "old", 3, 2}, {"revision", "owner", "old", 3, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			r, m := nativeMock(t)
			expectNativeSnapshot(m, "old")
			expectNativePublishLocks(m, tc.owner, tc.content, tc.fence, tc.revision)
			m.ExpectRollback()
			expectFail(m)
			e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
				return nativeStream([]NativeDetail{{Content: "detail"}}), nil
			})
			if !errors.Is(e, errWebLease) {
				t.Fatal(e)
			}
		})
	}
}
func expectNativePublishWrites(m sqlmock.Sqlmock, failInsert bool) {
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM logan_log_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if failInsert {
		m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectExec("INSERT INTO `logan_log_detail`").WillReturnError(errors.New("insert failed"))
		return
	}
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO `logan_log_detail`").WillReturnResult(sqlmock.NewResult(101, 1))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_task SET status=1").WithArgs(int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WithArgs(int64(8), "owner", int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
}
func TestNativeAtomicPublishAndRollback(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			r, m := nativeMock(t)
			expectNativeSnapshot(m, "old")
			expectNativePublishLocks(m, "owner", "old", 3, 2)
			expectNativePublishWrites(m, failure)
			if failure {
				m.ExpectRollback()
				expectFail(m)
			} else {
				m.ExpectCommit()
			}
			e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
				return nativeStream([]NativeDetail{{Content: "detail"}}), nil
			})
			if (e != nil) != failure {
				t.Fatal(e)
			}
		})
	}
}
func TestNativeAmbiguousCommitNeverDeletesAgain(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	expectNativePublishWrites(m, false)
	m.ExpectCommit().WillReturnError(errors.New("connection lost after commit"))
	m.ExpectExec("UPDATE logan_processing_job SET state=IF").WillReturnResult(sqlmock.NewResult(0, 0))
	e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
		return nativeStream([]NativeDetail{{Content: "detail"}}), nil
	})
	if e == nil {
		t.Fatal("lost commit response ignored")
	}
	m.ExpectExec("UPDATE logan_processing_job SET state='running'").WillReturnResult(sqlmock.NewResult(0, 0))
	if e := r.Process(context.Background(), 8, func(context.Context, NativeTask) (io.ReadCloser, error) {
		t.Fatal("published task reparsed")
		return nil, nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestNativeDoneSnapshotSkipsParser(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "owner", digestWeb("old"), 3, 2))
	m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(nativeSourceRows(1, "new upload"))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
		t.Fatal("completed source parsed")
		return nil, nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeExpiredLeaseCannotPublish(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	expectFail(m)
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
		return nativeStream([]NativeDetail{{Content: "detail"}}), nil
	}); !errors.Is(e, errWebLease) {
		t.Fatal(e)
	}
}
func TestNativePublicationBatchesAreBounded(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM logan_log_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO `logan_log_detail`.*VALUES " + strings.Repeat(`\([^)]*\),`, 199) + `\([^)]*\)$`).WillReturnResult(sqlmock.NewResult(100, 200))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec(`INSERT INTO ` + "`logan_log_detail`" + `.*VALUES \([^)]*\)$`).WillReturnResult(sqlmock.NewResult(300, 1))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_task SET status=1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
		return nativeStream(make([]NativeDetail, 201)), nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeHeartbeatLossCancelsParser(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST.*state='running'.*owner=.*fence=.*lease_until>").WithArgs(int64(8), "owner", int64(3)).WillReturnResult(sqlmock.NewResult(0, 0))
	expectFail(m)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e := r.processOwned(ctx, 8, "owner", func(ctx context.Context, _ NativeTask) (io.ReadCloser, error) { <-ctx.Done(); return nil, ctx.Err() })
	if !errors.Is(e, errWebLease) {
		t.Fatal(e)
	}
}

type nativeTrackedReader struct {
	io.Reader
	closed bool
}

func (r *nativeTrackedReader) Close() error { r.closed = true; return nil }
func TestNativeStreamIDsSanitizedAndClosed(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM logan_log_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO `logan_log_detail`.*task_id.*log_type.*content.*log_time").WithArgs(int64(8), 2, "ok", int64(3)).WillReturnResult(sqlmock.NewResult(100, 1))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_task SET status=1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	stream := &nativeTrackedReader{Reader: strings.NewReader(`{"ID":987,"TaskID":777,"LogType":2,"Content":"ok","LogTime":3}`)}
	e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) { return stream, nil })
	if e != nil || !stream.closed {
		t.Fatal(e, stream.closed)
	}
}
func TestNativeInvalidStreamRollsBackAndCloses(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM logan_log_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectRollback()
	expectFail(m)
	stream := &nativeTrackedReader{Reader: strings.NewReader("{broken")}
	e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) { return stream, nil })
	if e == nil || !stream.closed {
		t.Fatal(e, stream.closed)
	}
}
func TestNativeCleanupRetriesWithCanceledContext(t *testing.T) {
	r, m := nativeMock(t)
	cause := errors.New("parse failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ExpectExec("UPDATE logan_processing_job SET state=IF").WillReturnError(errors.New("transient"))
	m.ExpectExec("UPDATE logan_processing_job SET state=IF").WillReturnError(errors.New("transient"))
	expectFail(m)
	if e := r.fail(ctx, 8, "owner", 3, cause); !errors.Is(e, cause) {
		t.Fatal(e)
	}
}
func TestNativeSnapshotExpiredLeaseSkipsParser(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "owner", digestWeb("old"), 3, 2))
	m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(nativeSourceRows(0, "old"))
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	expectFail(m)
	e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
		t.Fatal("expired snapshot parsed")
		return nil, nil
	})
	if !errors.Is(e, errWebLease) {
		t.Fatal(e)
	}
}

func TestNativePublicationBatchesRespectByteBudget(t *testing.T) {
	r, m := nativeMock(t)
	expectNativeSnapshot(m, "old")
	expectNativePublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM logan_log_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	for i := 0; i < 3; i++ {
		m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectExec("INSERT INTO `logan_log_detail`.*VALUES " + `\([^)]*\)$`).WillReturnResult(sqlmock.NewResult(int64(100+i), 1))
	}
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=.* WHERE kind='native'.*state='running'.*owner=.*fence=\\?$").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_task SET status=1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	records := make([]NativeDetail, 3)
	for i := range records {
		records[i].Content = strings.Repeat("a", 600<<10)
	}
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) { return nativeStream(records), nil }); e != nil {
		t.Fatal(e)
	}
}
