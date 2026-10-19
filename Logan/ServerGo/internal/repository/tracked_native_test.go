package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"testing"
)

func objectRows(state string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"scope", "key_hash", "name", "state", "owner", "fence", "attempts"}).AddRow("scope", digestWeb("a"), "a", state, "owner", 3, 1)
}
func TestTrackedCreateRejectsCollectedAndDuplicateIntent(t *testing.T) {
	for _, state := range []string{"pending", "deleting", "deleted", "referenced", "failed"} {
		t.Run(state, func(t *testing.T) {
			r, m := maintenanceMock(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WithArgs("scope", digestWeb("a")).WillReturnRows(objectRows(state))
			m.ExpectRollback()
			name := "a"
			if NewTrackedNativeTasks(r.db, "scope").Create(context.Background(), &NativeTask{LogFileName: &name}) == nil {
				t.Fatal("unsafe task published")
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestTrackedCreateAtomicPublication(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("uploading"))
	m.ExpectExec("INSERT INTO `logan_task`").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("UPDATE logan_managed_object SET state='referenced'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	name := "a"
	if e := NewTrackedNativeTasks(r.db, "scope").Create(context.Background(), &NativeTask{LogFileName: &name}); e != nil {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestTrackedCreateRollbackAndAmbiguousCommit(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "update failure", true: "commit ack lost"}[ambiguous], func(t *testing.T) {
			r, m := maintenanceMock(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("uploading"))
			m.ExpectExec("INSERT INTO `logan_task`").WillReturnResult(sqlmock.NewResult(1, 1))
			if ambiguous {
				m.ExpectExec("UPDATE logan_managed_object").WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit().WillReturnError(errors.New("ack lost"))
			} else {
				m.ExpectExec("UPDATE logan_managed_object").WillReturnError(errors.New("db down"))
				m.ExpectRollback()
			}
			name := "a"
			if NewTrackedNativeTasks(r.db, "scope").Create(context.Background(), &NativeTask{LogFileName: &name}) == nil {
				t.Fatal("missing failure")
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
