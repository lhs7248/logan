package repository

import (
	"context"
	"errors"
	"logan/servergo/internal/maintenance"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func maintenanceMock(t *testing.T) (*Maintenance, sqlmock.Sqlmock) {
	r, m := webMock(t)
	return NewMaintenance(r.db), m
}
func TestMaintenanceBeginIsInsertOnly(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectExec("INSERT INTO logan_managed_object").WithArgs("scope", digestWeb("a"), "a").WillReturnError(errors.New("duplicate"))
	if r.BeginObject(context.Background(), "scope", "a") == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestMaintenanceRenewFenceLoss(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectExec("UPDATE logan_maintenance_lease.*owner=.*fence=.*lease_until>").WillReturnResult(sqlmock.NewResult(0, 0))
	if r.Renew(context.Background(), maintenanceLease(), time.Second) == nil {
		t.Fatal("stale fence accepted")
	}
}
func maintenanceLease() maintenance.Lease {
	return maintenance.Lease{Name: "gc", Owner: "owner", Fence: 2}
}

func expectMaintenanceLock(m sqlmock.Sqlmock, valid bool) {
	rows := sqlmock.NewRows([]string{"name"})
	if valid {
		rows.AddRow("gc")
	}
	m.ExpectQuery("SELECT name FROM logan_maintenance_lease.*FOR UPDATE").WithArgs("gc", "owner", int64(2)).WillReturnRows(rows)
}
func TestMaintenanceClaimScanFailureDoesNotDelete(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object").WillReturnError(errors.New("scan failed"))
	if _, e := r.Claim(context.Background(), maintenanceLease(), "scope", time.Second, 10); e == nil {
		t.Fatal("missing scan failure")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceClaimReferenceProtected(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object").WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("pending"))
	m.ExpectQuery("SELECT id FROM logan_task.*LIMIT 1").WithArgs("a").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	m.ExpectExec("UPDATE logan_managed_object SET state='referenced'").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	objects, e := r.Claim(context.Background(), maintenanceLease(), "scope", time.Second, 10)
	if e != nil || len(objects) != 0 {
		t.Fatal(objects, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceClaimRecoversExpiredDelete(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object.*state='deleting' AND lease_until<=").WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("deleting"))
	m.ExpectQuery("SELECT id FROM logan_task").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("UPDATE logan_managed_object SET state='deleting'.*fence=fence\\+1.*lease_until<=").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	objects, e := r.Claim(context.Background(), maintenanceLease(), "scope", time.Second, 10)
	if e != nil || len(objects) != 1 || objects[0].Fence != 4 {
		t.Fatal(objects, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceFinishFencedAndSanitized(t *testing.T) {
	for _, code := range []string{"", "provider secret"} {
		r, m := maintenanceMock(t)
		m.ExpectBegin()
		expectMaintenanceLock(m, true)
		if code == "" {
			m.ExpectExec("UPDATE logan_managed_object SET state='deleted',name=''.*owner=.*fence=.*lease_until>").WithArgs("scope", digestWeb("a"), "owner", int64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
		} else {
			m.ExpectExec("UPDATE logan_managed_object SET state=IF\\(attempts>=12,'failed','pending'\\).*last_error_code='delete_error'.*owner=.*fence=.*lease_until>").WithArgs(int64(1000), "scope", digestWeb("a"), "owner", int64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
		}
		m.ExpectRollback()
		if r.Finish(context.Background(), maintenanceLease(), maintenance.Object{Scope: "scope", Name: "a", Owner: "owner", Fence: 4}, code, time.Second) == nil {
			t.Fatal("stale ack accepted")
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMaintenanceFinishLeaseLossNoObjectMutation(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectBegin()
	expectMaintenanceLock(m, false)
	m.ExpectRollback()
	if r.Finish(context.Background(), maintenanceLease(), maintenance.Object{}, "", time.Second) == nil {
		t.Fatal("lost lease accepted")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceValidateMissingMigration(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT name,owner,fence,lease_until").WillReturnError(errors.New("missing"))
	if r.Validate(context.Background()) == nil {
		t.Fatal("missing migration accepted")
	}
}
func TestMaintenanceExpiredExhaustedClaimTerminal(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object").WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"scope", "key_hash", "name", "state", "attempts", "fence"}).AddRow("scope", digestWeb("a"), "a", "deleting", 12, 12))
	m.ExpectQuery("SELECT id FROM logan_task").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("UPDATE logan_managed_object SET state='failed'.*attempts>=12.*lease_until<=").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	v, e := r.Claim(context.Background(), maintenanceLease(), "scope", time.Second, 10)
	if e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func expectPruneSource(m sqlmock.Sqlmock, active, add int64) {
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectExec("INSERT IGNORE INTO logan_processing_job").WithArgs("native", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* AS active FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(active))
	if active == 0 {
		m.ExpectQuery("SELECT id,add_time,log_file_name FROM logan_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "add_time", "log_file_name"}).AddRow(7, add, "a"))
	}
}
func expectEmptyOrphans(m sqlmock.Sqlmock, detail string) {
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT d.id FROM " + detail + ".*LIMIT").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectCommit()
}
func expectPruneTail(m sqlmock.Sqlmock) {
	expectEmptyOrphans(m, "logan_log_detail")
	m.ExpectQuery("SELECT id FROM web_task WHERE add_time<.*ORDER BY id LIMIT").WithArgs(int64(100), 10).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectEmptyOrphans(m, "web_detail")
}
func TestMaintenancePruneActiveAndCutoffRecheck(t *testing.T) {
	for _, active := range []int64{0, 1} {
		r, m := maintenanceMock(t)
		m.ExpectQuery("SELECT id FROM logan_task WHERE add_time<.*ORDER BY id LIMIT").WithArgs(int64(100), 10).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
		expectPruneSource(m, active, 100)
		m.ExpectCommit()
		expectPruneTail(m)
		v, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
		if e != nil || v.NativeTasks != 0 || v.NativeDetails != 0 {
			t.Fatal(v, e)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func expectPruneAdoption(m sqlmock.Sqlmock) {
	m.ExpectExec("INSERT IGNORE INTO logan_managed_object").WithArgs("scope", digestWeb("a"), "a").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("referenced"))
	m.ExpectExec("UPDATE logan_managed_object SET state='pending'").WillReturnResult(sqlmock.NewResult(0, 1))
}
func TestMaintenancePruneDetailsBoundedAndAtomic(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT id FROM logan_task WHERE add_time<").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	expectPruneSource(m, 0, 99)
	expectPruneAdoption(m)
	m.ExpectExec("DELETE FROM logan_log_detail WHERE task_id=\\? LIMIT 500").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 500))
	m.ExpectExec("DELETE FROM logan_log_detail WHERE task_id=\\? LIMIT 500").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
	m.ExpectExec("DELETE FROM logan_task WHERE id=").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("DELETE FROM logan_processing_job WHERE kind=").WithArgs("native", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	expectPruneTail(m)
	v, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
	if e != nil || v.NativeTasks != 1 || v.NativeDetails != 502 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenancePruneRollbackNoCounts(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT id FROM logan_task WHERE add_time<").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	expectPruneSource(m, 0, 99)
	expectPruneAdoption(m)
	m.ExpectExec("DELETE FROM logan_log_detail").WillReturnResult(sqlmock.NewResult(0, 500))
	m.ExpectExec("DELETE FROM logan_log_detail").WillReturnError(errors.New("detail delete failed"))
	m.ExpectRollback()
	m.ExpectQuery("SELECT id FROM web_task").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectEmptyOrphans(m, "web_detail")
	v, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
	if e == nil || v.NativeTasks != 0 || v.NativeDetails != 0 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenancePruneCandidateScanFailureStopsNativeMutations(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT id FROM logan_task").WillReturnError(errors.New("scan failed"))
	m.ExpectQuery("SELECT id FROM web_task").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectEmptyOrphans(m, "web_detail")
	v, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
	if e == nil || v.NativeTasks != 0 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceSweepRechecksReferences(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		r, m := maintenanceMock(t)
		m.ExpectQuery("SELECT key_hash.*created_at<.*updated_at<.*LIMIT").WithArgs("scope", int64(100), int64(100), 10).WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
		m.ExpectBegin()
		expectMaintenanceLock(m, true)
		m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("uploading"))
		rows := sqlmock.NewRows([]string{"id"})
		if referenced {
			rows.AddRow(8)
		}
		m.ExpectQuery("SELECT id FROM logan_task").WithArgs("a").WillReturnRows(rows)
		if referenced {
			m.ExpectExec("UPDATE logan_managed_object SET state='referenced'").WillReturnResult(sqlmock.NewResult(0, 1))
		} else {
			m.ExpectExec("UPDATE logan_managed_object SET state='pending'").WillReturnResult(sqlmock.NewResult(0, 1))
		}
		m.ExpectCommit()
		n, e := r.Sweep(context.Background(), maintenanceLease(), "scope", 100, 10)
		if e != nil || (!referenced && n != 1) || (referenced && n != 0) {
			t.Fatal(n, e)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMaintenanceAcquireCASAndFence(t *testing.T) {
	for _, win := range []bool{false, true} {
		r, m := maintenanceMock(t)
		m.ExpectBegin()
		m.ExpectExec("INSERT IGNORE INTO logan_maintenance_lease").WillReturnResult(sqlmock.NewResult(0, 1))
		n := int64(0)
		if win {
			n = 1
		}
		m.ExpectExec("UPDATE logan_maintenance_lease.*fence=fence\\+1.*lease_until<=").WithArgs("owner", int64(300000), "gc").WillReturnResult(sqlmock.NewResult(0, n))
		if win {
			m.ExpectQuery("SELECT fence.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"fence"}).AddRow(4))
		}
		m.ExpectCommit()
		l, ok, e := r.Acquire(context.Background(), "gc", "owner", time.Hour)
		if e != nil || ok != win || (win && l.Fence != 4) {
			t.Fatal(l, ok, e)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMaintenanceFinishDeleteAckLossKeepsClaimRecoverable(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectExec("UPDATE logan_managed_object SET state='deleted',name=''").WillReturnError(errors.New("ack lost"))
	m.ExpectRollback()
	if r.Finish(context.Background(), maintenanceLease(), maintenance.Object{Scope: "scope", Name: "a", Owner: "owner", Fence: 4}, "", time.Second) == nil {
		t.Fatal("missing ack failure")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceFinishSuccessAndRetry(t *testing.T) {
	for _, code := range []string{"", "secret endpoint error"} {
		r, m := maintenanceMock(t)
		m.ExpectBegin()
		expectMaintenanceLock(m, true)
		if code == "" {
			m.ExpectExec("UPDATE logan_managed_object SET state='deleted',name='',owner='',lease_until=0,last_error_code=''.*owner=.*fence=.*lease_until>").WithArgs("scope", digestWeb("a"), "owner", int64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
		} else {
			m.ExpectExec("UPDATE logan_managed_object SET state=IF\\(attempts>=12,'failed','pending'\\).*last_error_code='delete_error'").WithArgs(int64(1000), "scope", digestWeb("a"), "owner", int64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		m.ExpectCommit()
		if e := r.Finish(context.Background(), maintenanceLease(), maintenance.Object{Scope: "scope", Name: "a", Owner: "owner", Fence: 4}, code, time.Second); e != nil {
			t.Fatal(e)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMaintenanceSweepDoesNotCollectRefreshedCandidate(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object").WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"scope", "key_hash", "name", "state", "created_at", "updated_at"}).AddRow("scope", digestWeb("a"), "a", "uploading", 1, 100))
	m.ExpectCommit()
	n, e := r.Sweep(context.Background(), maintenanceLease(), "scope", 100, 10)
	if e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceOrphanDetailsCleanupBoundedRecheck(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT id FROM logan_task WHERE add_time<").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT d.id FROM logan_log_detail.*ORDER BY d.id LIMIT").WithArgs(10).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(33))
	m.ExpectExec("DELETE FROM logan_log_detail WHERE id IN \\(\\?\\) AND NOT EXISTS").WithArgs(int64(33)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectQuery("SELECT id FROM web_task WHERE add_time<").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectEmptyOrphans(m, "web_detail")
	v, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
	if e != nil || v.NativeDetails != 1 || v.NativeTasks != 0 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceClaimStaleCandidatePreservesTombstone(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT key_hash FROM logan_managed_object").WillReturnRows(sqlmock.NewRows([]string{"key_hash"}).AddRow(digestWeb("a")))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectQuery("SELECT .* FROM logan_managed_object.*FOR UPDATE").WillReturnRows(objectRows("deleted"))
	m.ExpectCommit()
	v, e := r.Claim(context.Background(), maintenanceLease(), "scope", time.Second, 10)
	if e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestMaintenanceBoundScopeRejectsMutations(t *testing.T) {
	r, m := maintenanceMock(t)
	bound := NewMaintenance(r.db, "bound")
	ctx := context.Background()
	if bound.BeginObject(ctx, "other", "a") == nil {
		t.Fatal("cross-scope begin accepted")
	}
	if _, e := bound.Prune(ctx, maintenanceLease(), 100, 10, "other"); e == nil {
		t.Fatal("cross-scope prune accepted")
	}
	if _, e := bound.Sweep(ctx, maintenanceLease(), "other", 100, 10); e == nil {
		t.Fatal("cross-scope sweep accepted")
	}
	if _, e := bound.Claim(ctx, maintenanceLease(), "other", time.Second, 10); e == nil {
		t.Fatal("cross-scope claim accepted")
	}
	if bound.Finish(ctx, maintenanceLease(), maintenance.Object{Scope: "other"}, "", time.Second) == nil {
		t.Fatal("cross-scope finish accepted")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func expectMaintenanceBoundSchema(m sqlmock.Sqlmock) {
	for _, query := range []string{"SELECT name,owner,fence,lease_until FROM logan_maintenance_lease LIMIT 0", "SELECT scope,key_hash,name,state,owner,fence,lease_until,attempts,next_run_at,created_at,updated_at,last_error_code FROM logan_managed_object LIMIT 0", "SELECT kind,task_id,state,owner,fence,lease_until FROM logan_processing_job LIMIT 0", "SELECT id,add_time,log_file_name FROM logan_task LIMIT 0", "SELECT id,task_id FROM logan_log_detail LIMIT 0", "SELECT id,add_time FROM web_task LIMIT 0", "SELECT id,task_id FROM web_detail LIMIT 0", "SELECT id,scope FROM logan_storage_namespace LIMIT 0"} {
		m.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"probe"}))
	}
	rows := sqlmock.NewRows([]string{"TABLE_NAME", "ENGINE"})
	for _, name := range []string{"logan_maintenance_lease", "logan_managed_object", "logan_processing_job", "logan_task", "logan_log_detail", "web_task", "web_detail", "logan_storage_namespace"} {
		rows.AddRow(name, "InnoDB")
	}
	m.ExpectQuery("SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES").WillReturnRows(rows)
}
func TestMaintenanceNamespaceRejectsExistingOtherScope(t *testing.T) {
	r, m := maintenanceMock(t)
	bound := NewMaintenance(r.db, "bound")
	expectMaintenanceBoundSchema(m)
	m.ExpectBegin()
	m.ExpectQuery("SELECT scope FROM logan_managed_object WHERE scope<>.*LIMIT 1").WithArgs("bound").WillReturnRows(sqlmock.NewRows([]string{"scope"}).AddRow("old"))
	m.ExpectRollback()
	if bound.Validate(context.Background()) == nil {
		t.Fatal("existing foreign registry accepted")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceNamespaceSingletonCAS(t *testing.T) {
	for _, winner := range []bool{false, true} {
		r, m := maintenanceMock(t)
		bound := NewMaintenance(r.db, "bound")
		expectMaintenanceBoundSchema(m)
		m.ExpectBegin()
		m.ExpectQuery("SELECT scope FROM logan_managed_object WHERE scope<>.*LIMIT 1").WithArgs("bound").WillReturnRows(sqlmock.NewRows([]string{"scope"}))
		m.ExpectExec("INSERT IGNORE INTO logan_storage_namespace.*VALUES.*1.*").WithArgs("bound").WillReturnResult(sqlmock.NewResult(0, 0))
		stored := "other"
		if winner {
			stored = "bound"
		}
		m.ExpectQuery("SELECT scope FROM logan_storage_namespace WHERE id=1 FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"scope"}).AddRow(stored))
		if winner {
			m.ExpectCommit()
		} else {
			m.ExpectRollback()
		}
		e := bound.Validate(context.Background())
		if (e == nil) != winner {
			t.Fatal(winner, e)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestMaintenanceNativeBudgetStillAttemptsWeb(t *testing.T) {
	r, m := maintenanceMock(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.ExpectQuery("SELECT id FROM logan_task").WillReturnError(context.DeadlineExceeded)
	m.ExpectQuery("SELECT id FROM web_task").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectEmptyOrphans(m, "web_detail")
	_, e := r.Prune(ctx, maintenanceLease(), 100, 10, "scope")
	if e == nil {
		t.Fatal("native timeout hidden")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestMaintenanceNativeInvalidNameStillPrunesWeb(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT id FROM logan_task").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectExec("INSERT IGNORE INTO logan_processing_job").WithArgs("native", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* AS active FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(0))
	m.ExpectQuery("SELECT id,add_time,log_file_name FROM logan_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "add_time", "log_file_name"}).AddRow(7, 99, string([]byte{255})))
	m.ExpectRollback()
	m.ExpectQuery("SELECT id FROM web_task").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	m.ExpectBegin()
	expectMaintenanceLock(m, true)
	m.ExpectExec("INSERT IGNORE INTO logan_processing_job").WithArgs("web", int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* AS active FROM logan_processing_job.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(0))
	m.ExpectQuery("SELECT id,add_time FROM web_task.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "add_time"}).AddRow(9, 99))
	m.ExpectExec("DELETE FROM web_detail WHERE task_id=.*LIMIT 500").WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("DELETE FROM web_task WHERE id=").WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("DELETE FROM logan_processing_job WHERE kind=").WithArgs("web", int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	expectEmptyOrphans(m, "web_detail")
	counts, e := r.Prune(context.Background(), maintenanceLease(), 100, 10, "scope")
	if e == nil || counts.NativeTasks != 0 || counts.WebTasks != 1 || counts.WebDetails != 1 {
		t.Fatal(counts, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceSnapshotExcludesHistoricalStates(t *testing.T) {
	r, m := maintenanceMock(t)
	m.ExpectQuery("SELECT state,COUNT.*FROM logan_managed_object WHERE scope=\\? AND state IN \\('pending','deleting','uploading','failed'\\) GROUP BY state").WithArgs("scope").WillReturnRows(sqlmock.NewRows([]string{"state", "n"}).AddRow("pending", 4).AddRow("deleting", 2).AddRow("uploading", 3).AddRow("failed", 1))
	snapshot, e := r.Snapshot(context.Background(), "scope")
	if e != nil || snapshot.Pending != 4 || snapshot.Deleting != 2 || snapshot.Uploading != 3 || snapshot.Failed != 1 {
		t.Fatal(snapshot, e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
