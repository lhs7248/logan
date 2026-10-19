package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"testing"
)

func TestProcessingJobSnapshotAggregatesRetryAndResetsMissing(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT kind,state,COUNT\\(\\*\\) AS count FROM logan_processing_job.*GROUP BY kind,state").WillReturnRows(sqlmock.NewRows([]string{"kind", "state", "count"}).AddRow("native", "pending", 2).AddRow("native", "retry_wait", 3).AddRow("native", "running", 4).AddRow("web", "failed", 5).AddRow("native", "done", 999).AddRow("unknown-secret-kind", "pending", 999))
	got, e := ProcessingJobSnapshot(context.Background(), r.db)
	if e != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, e)
	}
	if got[0] != (JobCounts{Kind: "native", Pending: 5, Running: 4}) || got[1] != (JobCounts{Kind: "web", Failed: 5}) {
		t.Fatal(got)
	}
	m.ExpectQuery("SELECT kind,state").WillReturnRows(sqlmock.NewRows([]string{"kind", "state", "count"}))
	got, e = ProcessingJobSnapshot(context.Background(), r.db)
	if e != nil || len(got) != 2 || got[0].Pending != 0 || got[1].Failed != 0 {
		t.Fatalf("%+v %v", got, e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestProcessingJobSnapshotError(t *testing.T) {
	r, m := webMock(t)
	m.ExpectQuery("SELECT kind,state").WillReturnError(errors.New("database unavailable"))
	if v, e := ProcessingJobSnapshot(context.Background(), r.db); e == nil || v != nil {
		t.Fatalf("%+v %v", v, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := ProcessingJobSnapshot(ctx, r.db); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
