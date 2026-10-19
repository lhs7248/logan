package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"regexp"
	"testing"
)

func TestNativeLatestSelectsTwentyIDsBeforeServiceSort(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `logan_task` ORDER BY id DESC LIMIT ?")).WithArgs(20).WillReturnRows(sqlmock.NewRows([]string{"id", "log_date", "device_id"}).AddRow(5, nil, nil).AddRow(4, 100, "d"))
	got, e := r.Latest(context.Background())
	if e != nil || len(got) != 2 || got[0].LogDate != nil || got[0].DeviceID != nil {
		t.Fatal(got, e)
	}
}
func TestNativeSearchLegacyPlatformAndBudget(t *testing.T) {
	for _, platform := range []int{0, 1, 2, 3} {
		t.Run(string(rune('0'+platform)), func(t *testing.T) {
			r, m := nativeMock(t)
			r.maxBytes = 4096
			q := "SELECT * FROM `logan_task` WHERE (device_id = ? AND add_time BETWEEN ? AND ?)"
			if platform == 1 || platform == 2 {
				q += " AND platform = ?"
			} else {
				q = "SELECT * FROM `logan_task` WHERE device_id = ? AND add_time BETWEEN ? AND ?"
			}
			q += " ORDER BY id ASC LIMIT ?"
			expectation := m.ExpectQuery(regexp.QuoteMeta(q))
			if platform == 1 || platform == 2 {
				expectation.WithArgs("d", int64(1), int64(2), platform, 2)
			} else {
				expectation.WithArgs("d", int64(1), int64(2), 2)
			}
			expectation.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
			got, e := r.Search(context.Background(), "d", 1, 2, platform)
			if !errors.Is(e, ErrWebLimit) || got != nil {
				t.Fatal(got, e)
			}
		})
	}
}
func TestNativeIndexKeywordNilEmptyAndWildcard(t *testing.T) {
	empty, wild := "", "A%_"
	for _, keyword := range []*string{nil, &empty, &wild} {
		r, m := nativeMock(t)
		q := "SELECT id,log_type,log_time FROM logan_log_detail WHERE task_id=? AND log_type IN (?,?)"
		if keyword != nil {
			q += " AND content LIKE CONCAT('%',?,'%')"
		}
		q += " ORDER BY id ASC LIMIT ?"
		expectation := m.ExpectQuery(regexp.QuoteMeta(q))
		if keyword == nil {
			expectation.WithArgs(int64(8), 1, 2, 3)
		} else {
			expectation.WithArgs(int64(8), 1, 2, *keyword, 3)
		}
		expectation.WillReturnRows(sqlmock.NewRows([]string{"id", "log_type", "log_time"}).AddRow(9, 1, 100))
		got, e := r.MatchIndexes(context.Background(), 8, []int{1, 2}, keyword, 2)
		if e != nil || len(got) != 1 || got[0].Content != "" {
			t.Fatal(got, e)
		}
	}
}
func TestNativeZeroIndexLimit(t *testing.T) {
	for _, hasRow := range []bool{false, true} {
		r, m := nativeMock(t)
		rows := sqlmock.NewRows([]string{"id"})
		if hasRow {
			rows.AddRow(1)
		}
		m.ExpectQuery("SELECT id,log_type,log_time").WithArgs(int64(8), 1).WillReturnRows(rows)
		got, e := r.MatchIndexes(context.Background(), 8, nil, nil, 0)
		if hasRow {
			if !errors.Is(e, ErrWebLimit) {
				t.Fatal(e)
			}
		} else if e != nil || len(got) != 0 {
			t.Fatal(got, e)
		}
	}
}
func TestNativeBoundedDetailsPreflightAndGuard(t *testing.T) {
	r, m := nativeMock(t)
	m.ExpectQuery("SELECT id,COALESCE").WithArgs(int64(0), int64(2), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content_bytes"}).AddRow(0, 0).AddRow(2, 2))
	m.ExpectQuery("SELECT .* FROM logan_log_detail WHERE id=").WithArgs(int64(0), int64(2), int64(0)).WillReturnRows(sqlmock.NewRows([]string{"id", "content"}).AddRow(0, nil))
	m.ExpectQuery("SELECT .* FROM logan_log_detail WHERE id=").WithArgs(int64(2), int64(2), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content"}).AddRow(2, "ok"))
	got, e := r.BoundedDetails(context.Background(), []int64{0, 2, 2}, 2)
	if e != nil || len(got) != 2 || got[0].Content != "" || got[1].Content != "ok" {
		t.Fatal(got, e)
	}
}
func TestNativeBoundedDetailsNeverFalseEmpty(t *testing.T) {
	for _, growth := range []bool{false, true} {
		r, m := nativeMock(t)
		size := int64(3)
		if growth {
			size = 2
		}
		m.ExpectQuery("SELECT id,COALESCE").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "content_bytes"}).AddRow(1, size))
		if growth {
			m.ExpectQuery("SELECT .* FROM logan_log_detail WHERE id=").WithArgs(int64(1), int64(2), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "content"}))
		}
		got, e := r.BoundedDetails(context.Background(), []int64{1}, 2)
		if e == nil || got != nil {
			t.Fatal(got, e)
		}
	}
}
func TestNativeValidateExplicitSchemaAndEngines(t *testing.T) {
	for _, engine := range []string{"InnoDB", "MyISAM"} {
		r, m := nativeMock(t)
		m.ExpectQuery("SELECT kind,task_id").WillReturnRows(sqlmock.NewRows([]string{"kind"}))
		m.ExpectQuery("SELECT TABLE_NAME, ENGINE").WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "ENGINE"}).AddRow("logan_task", engine).AddRow("logan_log_detail", "InnoDB").AddRow("logan_processing_job", "InnoDB"))
		e := r.Validate(context.Background())
		if (e == nil) != (engine == "InnoDB") {
			t.Fatal(e)
		}
	}
}

func TestNativeSearchMinimumRowBudget(t *testing.T) {
	r, m := nativeMock(t)
	r.maxBytes = 1
	m.ExpectQuery("SELECT .* FROM `logan_task` WHERE device_id.*LIMIT \\?").WithArgs("d", int64(1), int64(2), 2).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	got, e := r.Search(context.Background(), "d", 1, 2, 0)
	if e != nil || len(got) != 1 {
		t.Fatal(got, e)
	}
}
