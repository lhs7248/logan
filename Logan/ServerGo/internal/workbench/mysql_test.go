package workbench

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLLeaseAndCompareAndSwap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	store, err := NewMySQLStore(g, "test")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT revision, payload FROM workbench_state").WithArgs("test").WillReturnRows(sqlmock.NewRows([]string{"revision", "payload"}).AddRow(3, `{"objects":{},"idempotency":{}}`))
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT IS_USED_LOCK").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"held"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE workbench_state").WithArgs(sqlmock.AnyArg(), int64(4), "test", int64(3)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err = store.Save(state); err == nil {
		t.Fatal("stale database revision accepted")
	}
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
