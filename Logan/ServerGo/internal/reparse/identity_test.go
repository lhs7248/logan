package reparse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"strings"
	"testing"
)

func TestIdentityWitnessRejectsTamperingWithoutDatabaseWrites(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1})
	w := IdentityWitness{Version: IdentityVersion, WitnessID: strings.Repeat("a", 32), SourceIdentity: "11111111-1111-1111-1111-111111111111/db", State: "pending"}
	if _, e := tool.MigrateIdentity(context.Background(), w, strings.Repeat("b", 64), "11111111-1111-1111-1111-111111111111/db", "22222222-2222-2222-2222-222222222222/db", false); e == nil {
		t.Fatal("accepted invalid witness")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestIdentityWitnessDigestBindsAllProofFields(t *testing.T) {
	w := IdentityWitness{Version: IdentityVersion, SourceGTID: "uuid:1-2"}
	w.Digest = witnessDigest(w)
	changed := w
	changed.SourceGTID = "uuid:1-3"
	if witnessDigest(changed) == w.Digest {
		t.Fatal("GTID not bound")
	}
}

func identityFixture() (Plan, snapshot) {
	s := snapshot{Task: map[string]any{"id": int64(7), "status": int64(1), "log_file_name": "object"}, Details: []map[string]any{{"id": int64(33), "task_id": int64(7), "content": "first"}}}
	p := Plan{OperationID: strings.Repeat("a", 32), TaskID: 7, ObjectName: "object", ObjectSHA256: strings.Repeat("c", 64), ExpectedDigest: strings.Repeat("d", 64), CompressedBytes: 1, PlainBytes: 1, BeforeHash: hash(s), DatabaseIdentity: "11111111-1111-1111-1111-111111111111/db", SchemaVersion: SchemaVersion, ToolVersion: ToolVersion, ExistingRecords: 1, ParsedRecords: 2, AppendRecords: 1}
	p.Fingerprint = fingerprint(p)
	return p, s
}
func proofExpectation(m sqlmock.Sqlmock, p Plan, s snapshot, a *proofAudit) {
	rows := sqlmock.NewRows([]string{"operation_id", "task_id", "fingerprint", "database_identity", "before_hash", "after_hash", "state", "before_sha", "after_sha", "append_sha", "append_ids", "after_ids"})
	if a != nil {
		rows.AddRow(a.OperationID, a.TaskID, a.Fingerprint, a.DatabaseIdentity, a.BeforeHash, a.AfterHash, a.State, a.BeforeSHA, a.AfterSHA, a.AppendSHA, a.AppendIDs, a.AfterIDs)
	}
	m.ExpectQuery("SELECT operation_id,task_id,fingerprint").WithArgs(p.OperationID).WillReturnRows(rows)
	m.ExpectQuery("SELECT \\* FROM logan_processing_job").WithArgs(p.TaskID).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("done"))
	m.ExpectQuery("SELECT \\* FROM logan_task").WithArgs(p.TaskID).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "log_file_name"}).AddRow(s.Task["id"], s.Task["status"], s.Task["log_file_name"]))
	details := sqlmock.NewRows([]string{"id", "task_id", "content"})
	for _, d := range s.Details {
		details.AddRow(d["id"], d["task_id"], d["content"])
	}
	m.ExpectQuery("SELECT \\* FROM logan_log_detail").WithArgs(p.TaskID).WillReturnRows(details)
}
func TestIdentityCurrentProofRejectsArchiveAndBusinessCorruption(t *testing.T) {
	for _, variant := range []string{"pending", "applied", "rolled_back", "bad_archive", "bad_ids", "bad_business", "active_audit"} {
		t.Run(variant, func(t *testing.T) {
			db, m := testDB(t)
			tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
			p, s := identityFixture()
			var a *proofAudit
			if variant != "pending" {
				after := snapshot{Task: s.Task, Details: append(append([]map[string]any{}, s.Details...), map[string]any{"id": int64(34), "task_id": int64(7), "content": "tail"})}
				a = &proofAudit{OperationID: p.OperationID, TaskID: p.TaskID, Fingerprint: p.Fingerprint, DatabaseIdentity: p.DatabaseIdentity, BeforeHash: p.BeforeHash, BeforeSHA: p.BeforeHash, AfterHash: hash(after), AfterSHA: hash(after), State: "applied", AppendSHA: rawHash("[34]"), AppendIDs: "[34]", AfterIDs: "[33,34]"}
				s = after
				switch variant {
				case "rolled_back":
					a.State = "rolled_back"
					_, s = identityFixture()
				case "bad_archive":
					a.BeforeSHA = strings.Repeat("c", 64)
				case "bad_ids":
					a.AppendIDs = "[35]"
				case "bad_business":
					s.Task["status"] = int64(2)
				case "active_audit":
					a.State = "applying"
				}
			}
			proofExpectation(m, p, s, a)
			_, _, _, e := tool.currentProof(db, p, false)
			wantOK := variant == "pending" || variant == "applied" || variant == "rolled_back"
			if (e == nil) != wantOK {
				t.Fatalf("error=%v expected success=%v", e, wantOK)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestIdentityOriginalUUIDNeedsNoAuthorizationMigration(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1})
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("11111111-1111-1111-1111-111111111111", "db"))
	if e := tool.authorize(db, "11111111-1111-1111-1111-111111111111/db", "op", "fp"); e != nil {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestIdentityCrossUUIDMissingMigrationRejects(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 1024, MaxLine: 1, MaxRecords: 1})
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("22222222-2222-2222-2222-222222222222", "db"))
	m.ExpectQuery("SELECT ENGINE").WithArgs(identityTable).WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}))
	if e := tool.authorize(db, "11111111-1111-1111-1111-111111111111/db", "op", "fp"); e == nil {
		t.Fatal("accepted missing table")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestIdentityMigrateRejectsUnexecutedGTIDBeforeWrites(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
	p, s := identityFixture()
	w := IdentityWitness{Version: IdentityVersion, Plan: p, SourceIdentity: p.DatabaseIdentity, SnapshotHash: hash(s), State: "pending", SourceGTID: "uuid:1-2", WitnessID: strings.Repeat("b", 32)}
	w.Digest = witnessDigest(w)
	m.ExpectBegin()
	expectEngines(m, true)
	m.ExpectQuery("SELECT ENGINE").WithArgs(identityTable).WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}).AddRow("InnoDB"))
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("22222222-2222-2222-2222-222222222222", "db"))
	m.ExpectQuery("SELECT GTID_SUBSET").WithArgs(w.SourceGTID).WillReturnRows(sqlmock.NewRows([]string{"subset"}).AddRow(0))
	m.ExpectRollback()
	if _, e := tool.MigrateIdentity(context.Background(), w, w.Digest, "11111111-1111-1111-1111-111111111111/db", "22222222-2222-2222-2222-222222222222/db", false); e == nil {
		t.Fatal("accepted lagged target")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestIdentityTrustedDigestSourceAndTargetRequired(t *testing.T) {
	p, s := identityFixture()
	w := IdentityWitness{Version: IdentityVersion, Plan: p, SourceIdentity: p.DatabaseIdentity, SnapshotHash: hash(s), State: "pending", SourceGTID: "uuid:1-2", WitnessID: strings.Repeat("b", 32)}
	w.Digest = witnessDigest(w)
	for _, args := range [][3]string{{strings.Repeat("a", 64), "11111111-1111-1111-1111-111111111111/db", "22222222-2222-2222-2222-222222222222/db"}, {w.Digest, "wrong/db", "22222222-2222-2222-2222-222222222222/db"}, {w.Digest, "11111111-1111-1111-1111-111111111111/db", "11111111-1111-1111-1111-111111111111/db"}, {w.Digest, "11111111-1111-1111-1111-111111111111/db", "target/other"}} {
		db, m := testDB(t)
		tool, _ := New(db, nil, Limits{MaxCompressed: 1, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
		if _, e := tool.MigrateIdentity(context.Background(), w, args[0], args[1], args[2], false); e == nil {
			t.Fatal("accepted invalid trust parameters")
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}

func rawHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestIdentityMigrationDryRunAppendOnlyAndStateEvolution(t *testing.T) {
	for _, mode := range []string{"dry", "write", "write_coerced", "write_task_changed", "repeat_applied", "repeat_rolled_back", "repeat_changed"} {
		t.Run(mode, func(t *testing.T) {
			db, m := testDB(t)
			tool, _ := New(db, nil, Limits{MaxCompressed: 8, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
			p, s := identityFixture()
			w := IdentityWitness{Version: IdentityVersion, Plan: p, SourceIdentity: p.DatabaseIdentity, SnapshotHash: hash(s), State: "pending", SourceGTID: "uuid:1-2", WitnessID: strings.Repeat("b", 32)}
			w.Digest = witnessDigest(w)
			m.ExpectBegin()
			expectEngines(m, true)
			m.ExpectQuery("SELECT ENGINE").WithArgs(identityTable).WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}).AddRow("InnoDB"))
			m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("22222222-2222-2222-2222-222222222222", "db"))
			m.ExpectQuery("SELECT GTID_SUBSET").WithArgs(w.SourceGTID).WillReturnRows(sqlmock.NewRows([]string{"subset"}).AddRow(1))
			rows := sqlmock.NewRows([]string{"operation_id", "target_identity", "fingerprint", "source_identity", "witness_id", "witness_digest", "witness_json", "authorization_digest"})
			var auditRow *proofAudit
			repeated := strings.HasPrefix(mode, "repeat")
			if repeated {
				raw, _ := json.Marshal(w)
				a := authorization{OperationID: p.OperationID, TargetIdentity: "22222222-2222-2222-2222-222222222222/db", Fingerprint: p.Fingerprint, SourceIdentity: p.DatabaseIdentity, WitnessID: w.WitnessID, WitnessDigest: w.Digest, WitnessJSON: string(raw)}
				a.AuthorizationDigest = authorizationDigest(a)
				rows.AddRow(a.OperationID, a.TargetIdentity, a.Fingerprint, a.SourceIdentity, a.WitnessID, a.WitnessDigest, a.WitnessJSON, a.AuthorizationDigest)
				after := snapshot{Task: s.Task, Details: append(append([]map[string]any{}, s.Details...), map[string]any{"id": int64(34), "task_id": int64(7), "content": "tail"})}
				auditRow = &proofAudit{OperationID: p.OperationID, TaskID: p.TaskID, Fingerprint: p.Fingerprint, DatabaseIdentity: p.DatabaseIdentity, BeforeHash: p.BeforeHash, BeforeSHA: p.BeforeHash, AfterHash: hash(after), AfterSHA: hash(after), State: "applied", AppendSHA: rawHash("[34]"), AppendIDs: "[34]", AfterIDs: "[33,34]"}
				if mode == "repeat_rolled_back" {
					auditRow.State = "rolled_back"
				} else {
					s = after
				}
				if mode == "repeat_changed" {
					s.Details[0]["content"] = "manual change"
				}
			}
			m.ExpectQuery("SELECT operation_id,target_identity").WithArgs(p.OperationID, "22222222-2222-2222-2222-222222222222/db").WillReturnRows(rows)
			proofExpectation(m, p, s, auditRow)
			if strings.HasPrefix(mode, "write") {
				m.ExpectExec("INSERT INTO `logan_native_reparse_identity_authorization`").WillReturnResult(sqlmock.NewResult(0, 1))
				raw, _ := json.Marshal(w)
				grant := authorization{OperationID: p.OperationID, TargetIdentity: "22222222-2222-2222-2222-222222222222/db", Fingerprint: p.Fingerprint, SourceIdentity: p.DatabaseIdentity, WitnessID: w.WitnessID, WitnessDigest: w.Digest, WitnessJSON: string(raw)}
				if mode == "write_coerced" {
					grant.TargetIdentity = "33333333-3333-3333-3333-333333333333/db"
				}
				grant.AuthorizationDigest = authorizationDigest(grant)
				m.ExpectQuery("SELECT operation_id,target_identity").WithArgs(p.OperationID, "22222222-2222-2222-2222-222222222222/db").WillReturnRows(sqlmock.NewRows([]string{"operation_id", "target_identity", "fingerprint", "source_identity", "witness_id", "witness_digest", "witness_json", "authorization_digest"}).AddRow(grant.OperationID, grant.TargetIdentity, grant.Fingerprint, grant.SourceIdentity, grant.WitnessID, grant.WitnessDigest, grant.WitnessJSON, grant.AuthorizationDigest))
				if mode != "write_coerced" {
					if mode == "write_task_changed" {
						s.Task["status"] = int64(2)
					}
					proofExpectation(m, p, s, auditRow)
				}
			}
			if mode == "repeat_changed" || mode == "write_coerced" || mode == "write_task_changed" {
				m.ExpectRollback()
			} else {
				m.ExpectCommit()
			}
			r, e := tool.MigrateIdentity(context.Background(), w, w.Digest, p.DatabaseIdentity, "22222222-2222-2222-2222-222222222222/db", mode == "dry")
			if (e != nil) != (mode == "repeat_changed" || mode == "write_coerced" || mode == "write_task_changed") {
				t.Fatalf("mode %s error %v", mode, e)
			}
			if e == nil && r.Noop != repeated {
				t.Fatalf("noop=%v", r.Noop)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestIdentityExportRejectsGTIDChange(t *testing.T) {
	db, m := testDB(t)
	tool, _ := New(db, nil, Limits{MaxCompressed: 8, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
	p, s := identityFixture()
	m.ExpectBegin()
	expectEngines(m, true)
	m.ExpectQuery("SELECT @@GLOBAL.gtid_executed").WillReturnRows(sqlmock.NewRows([]string{"gtid"}).AddRow("uuid:1"))
	m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("11111111-1111-1111-1111-111111111111", "db"))
	proofExpectation(m, p, s, nil)
	m.ExpectQuery("SELECT @@GLOBAL.gtid_executed").WillReturnRows(sqlmock.NewRows([]string{"gtid"}).AddRow("uuid:1-2"))
	m.ExpectRollback()
	if _, e := tool.ExportIdentity(context.Background(), p); e == nil {
		t.Fatal("accepted changing source GTID")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestIdentityAuthorizedOperationRejectsArchiveReplacementAndStateRegression(t *testing.T) {
	for _, mode := range []string{"applied", "rolled_back", "deleted_audit", "changed_archive"} {
		t.Run(mode, func(t *testing.T) {
			db, m := testDB(t)
			tool, _ := New(db, nil, Limits{MaxCompressed: 8, MaxPlain: 4096, MaxLine: 1, MaxRecords: 8})
			p, before := identityFixture()
			after := snapshot{Task: before.Task, Details: append(append([]map[string]any{}, before.Details...), map[string]any{"id": int64(34), "task_id": int64(7), "content": "tail"})}
			a := proofAudit{OperationID: p.OperationID, TaskID: p.TaskID, Fingerprint: p.Fingerprint, DatabaseIdentity: p.DatabaseIdentity, BeforeHash: p.BeforeHash, BeforeSHA: p.BeforeHash, AfterHash: hash(after), AfterSHA: hash(after), AppendSHA: rawHash("[34]"), AppendIDs: "[34]", AfterIDs: "[33,34]"}
			w := IdentityWitness{Version: IdentityVersion, Plan: p, SourceIdentity: p.DatabaseIdentity, SnapshotHash: hash(after), State: "applied", AuditHash: hash(a), SourceGTID: "uuid:1-2", WitnessID: strings.Repeat("b", 32)}
			w.Digest = witnessDigest(w)
			raw, _ := json.Marshal(w)
			grant := authorization{OperationID: p.OperationID, TargetIdentity: "22222222-2222-2222-2222-222222222222/db", Fingerprint: p.Fingerprint, SourceIdentity: p.DatabaseIdentity, WitnessID: w.WitnessID, WitnessDigest: w.Digest, WitnessJSON: string(raw)}
			grant.AuthorizationDigest = authorizationDigest(grant)
			m.ExpectQuery("SELECT @@server_uuid").WillReturnRows(sqlmock.NewRows([]string{"server", "name"}).AddRow("22222222-2222-2222-2222-222222222222", "db"))
			m.ExpectQuery("SELECT ENGINE").WithArgs(identityTable).WillReturnRows(sqlmock.NewRows([]string{"ENGINE"}).AddRow("InnoDB"))
			m.ExpectQuery("SELECT operation_id,target_identity").WithArgs(p.OperationID, grant.TargetIdentity).WillReturnRows(sqlmock.NewRows([]string{"operation_id", "target_identity", "fingerprint", "source_identity", "witness_id", "witness_digest", "witness_json", "authorization_digest"}).AddRow(grant.OperationID, grant.TargetIdentity, grant.Fingerprint, grant.SourceIdentity, grant.WitnessID, grant.WitnessDigest, grant.WitnessJSON, grant.AuthorizationDigest))
			current := after
			a.State = "applied"
			var row *proofAudit = &a
			switch mode {
			case "rolled_back":
				current = before
				a.State = "rolled_back"
			case "deleted_audit":
				current = before
				row = nil
			case "changed_archive":
				a.AppendIDs = "[35]"
				a.AppendSHA = rawHash("[35]")
			}
			proofExpectation(m, p, current, row)
			e := tool.authorize(db, p.DatabaseIdentity, p.OperationID, p.Fingerprint)
			if (e == nil) != (mode == "applied" || mode == "rolled_back") {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
