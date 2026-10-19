package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"regexp"
	"strings"
	"testing"
	"time"
)

func webMock(t *testing.T) (*WebRepository, sqlmock.Sqlmock) {
	t.Helper()
	p, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close() })
	d, e := gorm.Open(mysql.New(mysql.Config{Conn: p, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := m.ExpectationsWereMet(); e != nil {
			t.Error(e)
		}
	})
	return NewWebRepository(d), m
}
func TestWebEmptyReads(t *testing.T) {
	r, _ := webMock(t)
	ctx := context.Background()
	if v, e := r.Tasks(ctx, nil); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if v, e := r.Details(ctx, nil); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if v, e := r.Ready(ctx, nil); e != nil || !v {
		t.Fatal(v, e)
	}
}
func TestWebClaimCASLossDoesNotParse(t *testing.T) {
	r, m := webMock(t)
	m.ExpectExec("UPDATE logan_processing_job SET state='running'").WillReturnResult(sqlmock.NewResult(0, 0))
	called := false
	e := r.Process(context.Background(), 8, func(context.Context, WebTask) ([]WebDetail, error) { called = true; return nil, nil })
	if e != nil || called {
		t.Fatal(e, called)
	}
}
func TestWebEnsureAlreadyAnalyzed(t *testing.T) {
	r, m := webMock(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO logan_processing_job.*ON DUPLICATE KEY UPDATE task_id=task_id").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"task_id", "state"}).AddRow(4, "pending"))
	m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status", "content"}).AddRow(4, 1, "overwritten"))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.Ensure(context.Background(), 4); e != nil {
		t.Fatal(e)
	}
}
func TestWebFailedReady(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT .* FROM `web_task`").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(4, 0))
	m.ExpectQuery("SELECT .* FROM logan_processing_job").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("failed"))
	if _, e := r.Ready(context.Background(), []int64{4}); e == nil {
		t.Fatal("failed job accepted")
	}
}
func TestWebPublishedReadyPreserved(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT .* FROM `web_task`").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(4, 1))
	if v, e := r.Ready(context.Background(), []int64{4}); e != nil || !v {
		t.Fatal(v, e)
	}
}

func jobRows(state, owner, digest string, fence, revision int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"task_id", "state", "owner", "fence", "source_revision", "source_digest", "attempts"}).AddRow(8, state, owner, fence, revision, digest, 1)
}
func sourceRows(status int, content string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "status", "content"}).AddRow(8, status, content)
}

type captureOwner struct{ value *string }

func (c captureOwner) Match(v driver.Value) bool {
	s, ok := v.(string)
	if ok {
		*c.value = s
	}
	return ok && len(s) == 32
}

// sqlmock evaluates argument matchers when the claim executes. Subsequent dynamic
// row values are installed by the parser, after the snapshot has been consumed.
func expectClaim(m sqlmock.Sqlmock, owner *string) {
	m.ExpectExec("UPDATE logan_processing_job SET state='running'").WithArgs(captureOwner{owner}, int64(8), 3).WillReturnResult(sqlmock.NewResult(0, 1))
}
func expectFail(m sqlmock.Sqlmock) {
	m.ExpectExec("UPDATE logan_processing_job SET state=IF").WillReturnResult(sqlmock.NewResult(0, 1))
}

// The returned owner is intentionally mismatched: a takeover must prevent even parsing.
func TestWebSnapshotFenceLost(t *testing.T) {
	r, m := webMock(t)
	var owner string
	expectClaim(m, &owner)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "another-owner", digestWeb("a"), 2, 1))
	m.ExpectRollback()
	expectFail(m)
	called := false
	e := r.Process(context.Background(), 8, func(context.Context, WebTask) ([]WebDetail, error) { called = true; return nil, nil })
	if !errors.Is(e, errWebLease) || called {
		t.Fatal(e, called)
	}
}

func TestWebSaveAnalyzedOnlyUpdatesContent(t *testing.T) {
	r, m := webMock(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO logan_web_upload_lock.*ON DUPLICATE KEY UPDATE identity_key=identity_key").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT identity_key").WillReturnRows(sqlmock.NewRows([]string{"identity_key"}).AddRow("key"))
	m.ExpectQuery("SELECT .* FROM web_task WHERE log_date").WillReturnRows(sourceRows(1, "old"))
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("done", "", digestWeb("old"), 1, 1))
	m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status", "content", "add_time", "page_num"}).AddRow(8, 1, "old", 123, 2))
	m.ExpectExec("UPDATE web_task SET content=\\? WHERE id=\\?").WithArgs("new", int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	task := WebTask{Content: "new", AddTime: 999, PageNum: 2}
	if e := r.Save(context.Background(), &task); e != nil {
		t.Fatal(e)
	}
	if task.Status != 1 || task.AddTime != 123 || task.ID != 8 {
		t.Fatal(task)
	}
}
func TestWebSaveNormalInvalidatesWorker(t *testing.T) {
	r, m := webMock(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO logan_web_upload_lock.*ON DUPLICATE KEY UPDATE identity_key=identity_key").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT identity_key").WillReturnRows(sqlmock.NewRows([]string{"identity_key"}).AddRow("key"))
	m.ExpectQuery("SELECT .* FROM web_task WHERE log_date").WillReturnRows(sourceRows(0, "old"))
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "worker", digestWeb("old"), 1, 1))
	m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sourceRows(0, "old"))
	m.ExpectExec("UPDATE web_task SET content").WithArgs("new", int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET source_revision=source_revision\\+1,source_digest=\\?,fence=fence\\+1,state='pending'").WithArgs(digestWeb("new"), int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.Save(context.Background(), &WebTask{Content: "new"}); e != nil {
		t.Fatal(e)
	}
}
func TestWebPendingExpiredRecovery(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT task_id,owner,fence,lease_until FROM logan_processing_job").WithArgs("web", 3, 20).WillReturnRows(sqlmock.NewRows([]string{"task_id", "owner", "fence", "lease_until"}).AddRow(7, "old", 4, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='failed'.*WHERE kind=.*AND task_id=.*AND state='running'.*AND owner=.*AND fence=.*AND lease_until=.*AND lease_until<=.*AND attempts>=").WithArgs("web", int64(7), "old", int64(4), int64(1), 3).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT task_id.*state='running' AND lease_until<=").WithArgs(3, 20).WillReturnRows(sqlmock.NewRows([]string{"task_id"}).AddRow(8))
	v, e := r.Pending(context.Background(), 20)
	if e != nil || len(v) != 1 || v[0] != 8 {
		t.Fatal(v, e)
	}
}

func expectSnapshot(m sqlmock.Sqlmock, content string) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "owner", digestWeb(content), 3, 2))
	m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sourceRows(0, content))
	m.ExpectCommit()
}
func expectPublishLocks(m sqlmock.Sqlmock, owner, content string, fence, revision int64) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", owner, digestWeb("old"), fence, revision))
	if owner == "owner" && fence == 3 && revision == 2 {
		m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sourceRows(0, content))
	}
}
func TestWebParseFailureRetry(t *testing.T) {
	r, m := webMock(t)
	expectSnapshot(m, "old")
	expectFail(m)
	bad := errors.New("bad source")
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return nil, bad }); !errors.Is(e, bad) {
		t.Fatal(e)
	}
}
func TestWebPublishRejectsStaleSourceAndFence(t *testing.T) {
	for _, tc := range []struct {
		name, owner, content string
		fence, revision      int64
	}{{"source", "owner", "new", 3, 2}, {"fence", "owner", "old", 4, 2}, {"owner", "other", "old", 3, 2}, {"revision", "owner", "old", 3, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			r, m := webMock(t)
			expectSnapshot(m, "old")
			expectPublishLocks(m, tc.owner, tc.content, tc.fence, tc.revision)
			m.ExpectRollback()
			expectFail(m)
			e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return []WebDetail{{Content: "detail"}}, nil })
			if !errors.Is(e, errWebLease) {
				t.Fatal(e)
			}
		})
	}
}
func expectPublishWrites(m sqlmock.Sqlmock, failInsert bool) {
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM web_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if failInsert {
		m.ExpectExec("INSERT INTO `web_detail`").WillReturnError(errors.New("insert failed"))
		return
	}
	m.ExpectExec("INSERT INTO `web_detail`").WillReturnResult(sqlmock.NewResult(101, 1))
	m.ExpectExec("UPDATE web_task SET status=1").WithArgs(int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WithArgs(int64(8), "owner", int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
}
func TestWebAtomicPublishAndRollback(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			r, m := webMock(t)
			expectSnapshot(m, "old")
			expectPublishLocks(m, "owner", "old", 3, 2)
			expectPublishWrites(m, failure)
			if failure {
				m.ExpectRollback()
				expectFail(m)
			} else {
				m.ExpectCommit()
			}
			e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return []WebDetail{{Content: "detail"}}, nil })
			if (e != nil) != failure {
				t.Fatal(e)
			}
		})
	}
}
func TestWebAmbiguousCommitNeverDeletesAgain(t *testing.T) {
	r, m := webMock(t)
	expectSnapshot(m, "old")
	expectPublishLocks(m, "owner", "old", 3, 2)
	expectPublishWrites(m, false)
	m.ExpectCommit().WillReturnError(errors.New("connection lost after commit"))
	m.ExpectExec("UPDATE logan_processing_job SET state=IF").WillReturnResult(sqlmock.NewResult(0, 0))
	e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return []WebDetail{{Content: "detail"}}, nil })
	if e == nil {
		t.Fatal("lost commit response ignored")
	}
	m.ExpectExec("UPDATE logan_processing_job SET state='running'").WillReturnResult(sqlmock.NewResult(0, 0))
	if e := r.Process(context.Background(), 8, func(context.Context, WebTask) ([]WebDetail, error) {
		t.Fatal("published task reparsed")
		return nil, nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestWebDoneSnapshotSkipsParser(t *testing.T) {
	r, m := webMock(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(jobRows("running", "owner", digestWeb("old"), 3, 2))
	m.ExpectQuery("SELECT .* FROM web_task.*FOR UPDATE").WillReturnRows(sourceRows(1, "new upload"))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) {
		t.Fatal("completed source parsed")
		return nil, nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestWebExpiredLeaseCannotPublish(t *testing.T) {
	r, m := webMock(t)
	expectSnapshot(m, "old")
	expectPublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	expectFail(m)
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return []WebDetail{{Content: "detail"}}, nil }); !errors.Is(e, errWebLease) {
		t.Fatal(e)
	}
}
func TestWebValidateRequiresExplicitMigrationAndInnoDB(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		missing, nontransactional bool
	}{{"valid", false, false}, {"missing", true, false}, {"nontransactional", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r, m := webMock(t)
			if tc.missing {
				m.ExpectQuery("SELECT kind,task_id").WillReturnError(errors.New("missing table"))
			} else {
				m.ExpectQuery("SELECT kind,task_id").WillReturnRows(sqlmock.NewRows([]string{"kind"}))
				m.ExpectQuery("SELECT identity_key").WillReturnRows(sqlmock.NewRows([]string{"identity_key"}))
				engine := "InnoDB"
				if tc.nontransactional {
					engine = "MyISAM"
				}
				m.ExpectQuery("SELECT TABLE_NAME, ENGINE").WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "ENGINE"}).AddRow("web_task", engine).AddRow("web_detail", "InnoDB").AddRow("logan_processing_job", "InnoDB").AddRow("logan_web_upload_lock", "InnoDB"))
			}
			err := r.Validate(context.Background())
			if (err != nil) != (tc.missing || tc.nontransactional) {
				t.Fatal(err)
			}
		})
	}
}

func TestWebPublicationBatchesAreBounded(t *testing.T) {
	r, m := webMock(t)
	expectSnapshot(m, "old")
	expectPublishLocks(m, "owner", "old", 3, 2)
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT id FROM web_detail WHERE task_id=").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("INSERT INTO `web_detail`.*VALUES " + strings.Repeat(`\([^)]*\),`, 199) + `\([^)]*\)$`).WillReturnResult(sqlmock.NewResult(100, 200))
	m.ExpectExec(`INSERT INTO ` + "`web_detail`" + `.*VALUES \([^)]*\)$`).WillReturnResult(sqlmock.NewResult(300, 1))
	m.ExpectExec("UPDATE web_task SET status=1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE logan_processing_job SET state='done'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := r.processOwned(context.Background(), 8, "owner", func(context.Context, WebTask) ([]WebDetail, error) { return make([]WebDetail, 201), nil }); e != nil {
		t.Fatal(e)
	}
}

func TestWebContentsRejectsOversizeBeforeBody(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,page_num,COALESCE\\(OCTET_LENGTH\\(content\\),0\\) AS content_bytes FROM web_task").WithArgs(int64(8), int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id", "page_num", "content_bytes"}).AddRow(8, 1, 4).AddRow(9, 2, 7))
	if _, e := r.Contents(context.Background(), []int64{8, 9}, 10); e == nil {
		t.Fatal("oversize accepted")
	}
}
func TestWebContentsSQLGuardsAndPageOrder(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,page_num,COALESCE\\(OCTET_LENGTH\\(content\\),0\\) AS content_bytes FROM web_task WHERE id IN \\(\\?,\\?\\) ORDER BY page_num ASC,id ASC").WithArgs(int64(9), int64(8)).WillReturnRows(sqlmock.NewRows([]string{"id", "page_num", "content_bytes"}).AddRow(8, 1, 3).AddRow(9, 2, 4))
	m.ExpectQuery("SELECT content FROM web_task WHERE id=\\? AND COALESCE\\(OCTET_LENGTH\\(content\\),0\\)<=\\? AND COALESCE\\(OCTET_LENGTH\\(content\\),0\\)=\\?").WithArgs(int64(8), int64(7), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("one"))
	m.ExpectQuery("SELECT content FROM web_task WHERE id=").WithArgs(int64(9), int64(4), int64(4)).WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("two!"))
	v, e := r.Contents(context.Background(), []int64{9, 8}, 7)
	if e != nil || string(v) != "onetwo!" {
		t.Fatal(string(v), e)
	}
}
func TestWebContentsRejectsConcurrentSizeChange(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,page_num").WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"id", "page_num", "content_bytes"}).AddRow(8, 1, 3))
	m.ExpectQuery("SELECT content FROM web_task WHERE id=").WithArgs(int64(8), int64(4), int64(3)).WillReturnRows(sqlmock.NewRows([]string{"content"}))
	if _, e := r.Contents(context.Background(), []int64{8}, 4); e == nil {
		t.Fatal("concurrent deletion/growth accepted")
	}
}

func TestWebMetadataReadsOmitBody(t *testing.T) {
	r, m := webMock(t)
	fields := "SELECT `web_task`.`id`,`web_task`.`device_id`,`web_task`.`web_source`,`web_task`.`environment`,`web_task`.`page_num`,`web_task`.`add_time`,`web_task`.`log_date`,`web_task`.`status`,`web_task`.`custom_report_info`,`web_task`.`update_time` FROM `web_task`"
	m.ExpectQuery(regexp.QuoteMeta(fields + " WHERE id IN (?) ORDER BY id ASC")).WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	if _, e := r.Tasks(context.Background(), []int64{8}); e != nil {
		t.Fatal(e)
	}
	m.ExpectQuery(regexp.QuoteMeta(fields+" WHERE add_time BETWEEN ? AND ? AND device_id = ? ORDER BY id ASC LIMIT ?")).WithArgs(int64(1), int64(2), "device", 8193).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, e := r.Search(context.Background(), 1, 2, "device"); e != nil {
		t.Fatal(e)
	}
	m.ExpectQuery("SELECT COALESCE\\(MAX\\(id\\),0\\)").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	m.ExpectQuery(regexp.QuoteMeta(fields+" WHERE id BETWEEN ? AND ? ORDER BY id ASC")).WithArgs(int64(0), int64(100)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, e := r.Latest(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestWebContentsEmptyNoSQL(t *testing.T) {
	r, _ := webMock(t)
	if v, e := r.Contents(context.Background(), nil, 10); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
}

func TestWebMatchIndexesLiteralBinaryKeywordAndMetadata(t *testing.T) {
	r, m := webMock(t)
	q := "SELECT id,log_type,log_time FROM web_detail WHERE task_id=? AND minute_offset BETWEEN ? AND ? AND log_type IN (?,?) AND INSTR(BINARY content, BINARY ?) > 0 ORDER BY id ASC LIMIT ?"
	m.ExpectQuery(regexp.QuoteMeta(q)).WithArgs(int64(8), 0, 1440, 1, 2, "A%_", 3).WillReturnRows(sqlmock.NewRows([]string{"id", "log_type", "log_time"}).AddRow(9, 1, 100))
	v, e := r.MatchIndexes(context.Background(), 8, 0, 1440, []int{1, 2}, "A%_", 2)
	if e != nil || len(v) != 1 || v[0].Content != "" || v[0].ID != 9 {
		t.Fatal(v, e)
	}
}
func TestWebMatchIndexesRejectsOverflowWithoutBody(t *testing.T) {
	r, m := webMock(t)
	q := "SELECT id,log_type,log_time FROM web_detail WHERE task_id=? AND minute_offset BETWEEN ? AND ? ORDER BY id ASC LIMIT ?"
	m.ExpectQuery(regexp.QuoteMeta(q)).WithArgs(int64(8), 0, 1440, 2).WillReturnRows(sqlmock.NewRows([]string{"id", "log_type", "log_time"}).AddRow(9, 1, 100).AddRow(10, 1, 101))
	if _, e := r.MatchIndexes(context.Background(), 8, 0, 1440, nil, "", 1); e == nil {
		t.Fatal("index budget overflow accepted")
	}
}

func TestWebUploadLockIgnoresDeviceCollation(t *testing.T) {
	r, m := webMock(t)
	for _, device := range []string{"D", "d", "Ḋ"} {
		m.ExpectBegin()
		m.ExpectExec("INSERT INTO logan_web_upload_lock.*ON DUPLICATE KEY UPDATE identity_key=identity_key").WithArgs(digestWeb("[4,2]")).WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectQuery("SELECT identity_key").WithArgs(digestWeb("[4,2]")).WillReturnRows(sqlmock.NewRows([]string{"identity_key"}).AddRow(digestWeb("[4,2]")))
		m.ExpectQuery("SELECT .* FROM web_task WHERE log_date").WithArgs(int64(4), device, 2).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		m.ExpectExec("INSERT INTO `web_task`").WillReturnResult(sqlmock.NewResult(8, 1))
		m.ExpectCommit()
		if e := r.Save(context.Background(), &WebTask{DeviceID: device, LogDate: 4, PageNum: 2, Content: "body"}); e != nil {
			t.Fatal(e)
		}
	}
}

func TestWebBoundedDetailsOversizeBeforeBodies(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,COALESCE\\(OCTET_LENGTH\\(content\\),0\\) AS content_bytes FROM web_detail").WithArgs(int64(1), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content_bytes"}).AddRow(1, 6).AddRow(2, 6))
	if _, e := r.BoundedDetails(context.Background(), []int64{1, 2}, 10); e == nil {
		t.Fatal("detail byte overflow accepted")
	}
}
func TestWebBoundedDetailsGrowthRejected(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,COALESCE").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "content_bytes"}).AddRow(1, 2))
	m.ExpectQuery("SELECT .* FROM web_detail WHERE id=\\? AND COALESCE\\(OCTET_LENGTH\\(content\\),0\\)<=\\? AND COALESCE\\(OCTET_LENGTH\\(content\\),0\\)=\\?").WithArgs(int64(1), int64(3), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content"}))
	if _, e := r.BoundedDetails(context.Background(), []int64{1}, 3); e == nil {
		t.Fatal("grown/deleted detail accepted")
	}
}
func TestWebBoundedDetailsNormalAndNull(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT id,COALESCE").WithArgs(int64(1), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content_bytes"}).AddRow(1, 2).AddRow(2, 0))
	m.ExpectQuery("SELECT .* FROM web_detail WHERE id=").WithArgs(int64(1), int64(2), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "content", "log_level"}).AddRow(1, 8, "ok", nil))
	m.ExpectQuery("SELECT .* FROM web_detail WHERE id=").WithArgs(int64(2), int64(0), int64(0)).WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "content", "log_level"}).AddRow(2, 8, nil, nil))
	v, e := r.BoundedDetails(context.Background(), []int64{1, 2}, 2)
	if e != nil || len(v) != 2 || v[0].Content != "ok" || v[1].Content != "" || v[1].LogLevel != nil {
		t.Fatal(v, e)
	}
}
func TestWebHeartbeatLossCancelsParser(t *testing.T) {
	r, m := webMock(t)
	expectSnapshot(m, "old")
	m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST.*state='running'.*owner=.*fence=.*lease_until>").WithArgs(int64(8), "owner", int64(3)).WillReturnResult(sqlmock.NewResult(0, 0))
	expectFail(m)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e := r.processOwned(ctx, 8, "owner", func(ctx context.Context, _ WebTask) ([]WebDetail, error) { <-ctx.Done(); return nil, ctx.Err() })
	if !errors.Is(e, errWebLease) {
		t.Fatal(e)
	}
}

func TestWebSearchBudgetNeverTruncates(t *testing.T) {
	r, m := webMock(t)
	r.maxBytes = 4096
	m.ExpectQuery("SELECT .* FROM `web_task` WHERE add_time BETWEEN.*LIMIT \\?").WithArgs(int64(1), int64(2), "device", 2).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8).AddRow(9))
	v, e := r.Search(context.Background(), 1, 2, "device")
	if !errors.Is(e, ErrWebLimit) || v != nil {
		t.Fatal(v, e)
	}
}
func TestWebSearchMinimumRowBudget(t *testing.T) {
	r, m := webMock(t)
	r.maxBytes = 1
	m.ExpectQuery("SELECT .* FROM `web_task` WHERE add_time BETWEEN.*LIMIT \\?").WithArgs(int64(1), int64(2), "device", 2).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	v, e := r.Search(context.Background(), 1, 2, "device")
	if e != nil || len(v) != 1 {
		t.Fatal(v, e)
	}
}
func TestWebRepositoryConfiguredBudget(t *testing.T) {
	r, _ := webMock(t)
	if r.maxBytes != 32<<20 {
		t.Fatal(r.maxBytes)
	}
	configured := NewWebRepository(r.db, 2048)
	if configured.maxBytes != 2048 {
		t.Fatal(configured.maxBytes)
	}
}

func TestWebIndexAtBudgetAllowsEmptyNextTask(t *testing.T) {
	for _, hasRow := range []bool{false, true} {
		t.Run(fmt.Sprint(hasRow), func(t *testing.T) {
			r, m := webMock(t)
			rows := sqlmock.NewRows([]string{"id", "log_type", "log_time"})
			if hasRow {
				rows.AddRow(1, 1, 1)
			}
			m.ExpectQuery("SELECT id,log_type,log_time FROM web_detail").WithArgs(int64(2), 0, 1439, 1).WillReturnRows(rows)
			got, err := r.MatchIndexes(context.Background(), 2, 0, 1439, nil, "", 0)
			if hasRow {
				if !errors.Is(err, ErrWebLimit) {
					t.Fatalf("overflow=%v", err)
				}
			} else if err != nil || len(got) != 0 {
				t.Fatalf("empty tail rejected: %+v %v", got, err)
			}
		})
	}
}
