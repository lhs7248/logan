package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"io"
	"logan/servergo/internal/processingtime"
	"testing"
)

func TestNativeTimingUsesDatabaseClockAndOmitsRetryQueueAge(t *testing.T) {
	for _, attempt := range []int{1, 2} {
		r, m := nativeMock(t)
		m.ExpectBegin()
		m.ExpectQuery("SELECT .* FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"task_id", "state", "owner", "fence", "source_revision", "source_digest", "attempts", "created_at", "observed_at"}).AddRow(8, "running", "owner", 3, 2, digestWeb("old"), attempt, 100, 110))
		m.ExpectQuery("SELECT .* FROM logan_task.*FOR UPDATE").WillReturnRows(nativeSourceRows(0, "old"))
		m.ExpectExec("UPDATE logan_processing_job SET lease_until=GREATEST.*lease_until>").WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectCommit()
		expectNativePublishLocks(m, "owner", "old", 3, 2)
		expectNativePublishWrites(m, false)
		m.ExpectCommit()
		ctx, trace := processingtime.New(context.Background())
		err := r.processOwned(ctx, 8, "owner", func(context.Context, NativeTask) (io.ReadCloser, error) {
			return nativeStream([]NativeDetail{{Content: "detail"}}), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		stages := trace.Milliseconds()
		if attempt == 1 && stages["queue"] != 10 {
			t.Fatal("queue mixed app/DB clocks", stages)
		}
		if attempt == 2 {
			if _, exists := stages["queue"]; exists {
				t.Fatal("retry age mislabeled queue", stages)
			}
		}
		for _, key := range []string{"stage_decode", "db_insert", "publication"} {
			if stages[key] <= 0 {
				t.Fatal("missing publication stage", stages)
			}
		}
		if stages["publication"] < stages["db_insert"] {
			t.Fatal("publication must include insert", stages)
		}
		if err := m.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
