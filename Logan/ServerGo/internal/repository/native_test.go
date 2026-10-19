package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"regexp"
	"testing"
)

func TestNativeTaskGORMInsertAndDownloadLookup(t *testing.T) {
	pool, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewNativeTasks(db)
	name := "app_device_0_uuid.log"
	amount := "10"
	date := int64(0)
	task := &NativeTask{Amount: &amount, LogFileName: &name, LogDate: &date, AddTime: 123}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `logan_task`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	if err := repo.Create(context.Background(), task); err != nil || task.ID != 7 {
		t.Fatalf("insert: %v id=%d", err, task.ID)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `logan_task` WHERE id = ? ORDER BY `logan_task`.`id` LIMIT ?")).WithArgs(int64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "log_file_name"}).AddRow(7, name))
	got, err := repo.Find(context.Background(), 7)
	if err != nil || got.LogFileName == nil || *got.LogFileName != name {
		t.Fatalf("find: %+v %v", got, err)
	}
	mock.ExpectQuery("SELECT ").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	got, err = repo.Find(context.Background(), 99)
	if err != nil || got != nil {
		t.Fatal("missing task did not return nil")
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `logan_task`").WillReturnError(errors.New("db failure"))
	mock.ExpectRollback()
	if repo.Create(context.Background(), task) == nil {
		t.Fatal("insert failure ignored")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
