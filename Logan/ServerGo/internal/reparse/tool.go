// Package reparse implements explicitly invoked, audited repair of completed native tasks.
package reparse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
	"logan/servergo/internal/protocol"
)

const ToolVersion = "native-reparse-v1"
const SchemaVersion = "003"
const auditTable = "logan_native_reparse_operation"

type Limits struct {
	MaxCompressed, MaxPlain int64
	// MaxSnapshot bounds canonical JSON bytes across the complete task and detail snapshot. Zero defaults to four times MaxPlain, capped at 256 MiB.
	MaxSnapshot         int64
	MaxLine, MaxRecords int
	TempDir             string
}
type ObjectReader interface {
	Get(context.Context, string) (io.ReadCloser, error)
}
type Tool struct {
	db      *gorm.DB
	objects ObjectReader
	limits  Limits
}
type Plan struct {
	OperationID      string `json:"operation_id"`
	TaskID           int64  `json:"task_id"`
	ObjectName       string `json:"object_name"`
	ObjectSHA256     string `json:"object_sha256"`
	ExpectedDigest   string `json:"expected_digest"`
	BeforeHash       string `json:"before_hash"`
	Fingerprint      string `json:"fingerprint"`
	DatabaseIdentity string `json:"database_identity"`
	SchemaVersion    string `json:"schema_version"`
	ToolVersion      string `json:"tool_version"`
	ExistingRecords  int    `json:"existing_records"`
	ParsedRecords    int    `json:"parsed_records"`
	AppendRecords    int    `json:"append_records"`
	CompressedBytes  int64  `json:"compressed_bytes"`
	PlainBytes       int64  `json:"plain_bytes"`
}
type Result struct {
	OperationID     string `json:"operation_id"`
	TaskID          int64  `json:"task_id"`
	State           string `json:"state"`
	AppendedRecords int    `json:"appended_records"`
	Noop            bool   `json:"noop"`
}
type snapshot struct {
	Task    map[string]any
	Details []map[string]any
}
type audit struct {
	OperationID                                                                                           string
	TaskID                                                                                                int64
	Fingerprint, DatabaseIdentity, BeforeHash, AfterHash, BeforeSnapshot, AfterSnapshot, AppendIDs, State string
}

func (audit) TableName() string { return auditTable }
func New(db *gorm.DB, objects ObjectReader, l Limits) (*Tool, error) {
	if db == nil || l.MaxCompressed <= 0 || l.MaxPlain <= 0 || l.MaxLine <= 0 || l.MaxRecords <= 0 || l.MaxRecords == math.MaxInt || l.MaxCompressed == math.MaxInt64 || l.MaxPlain > math.MaxInt64/4 || l.MaxSnapshot < 0 || l.MaxSnapshot > 256<<20 {
		return nil, errors.New("database and positive resource limits required")
	}
	if l.MaxSnapshot == 0 {
		l.MaxSnapshot = min(l.MaxPlain*4, 256<<20)
	}
	return &Tool{db, objects, l}, nil
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func fingerprint(p Plan) string { p.Fingerprint = ""; return hash(p) }
func boundedRows(tx *gorm.DB, maxRecords int, maxBytes int64, q string, args ...any) ([]map[string]any, error) {
	r, e := tx.Raw(q, args...).Rows()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	cols, e := r.Columns()
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for r.Next() {
		if len(out) >= maxRecords {
			return nil, protocol.ErrLimit
		}
		vals := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vals {
			ptr[i] = &vals[i]
		}
		if e = r.Scan(ptr...); e != nil {
			return nil, e
		}
		m := map[string]any{}
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			m[c] = v
		}
		encoded, e := json.Marshal(m)
		if e != nil {
			return nil, e
		}
		if int64(len(encoded)) > maxBytes {
			return nil, protocol.ErrLimit
		}
		maxBytes -= int64(len(encoded))
		out = append(out, m)
	}
	return out, r.Err()
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func number(v any) int64 { n, _ := strconv.ParseInt(str(v), 10, 64); return n }
func identity(tx *gorm.DB) (string, error) {
	var x struct {
		Server string
		Name   string
	}
	e := tx.Raw("SELECT @@server_uuid AS server, DATABASE() AS name").Scan(&x).Error
	if e != nil {
		return "", e
	}
	if x.Server == "" || x.Name == "" {
		return "", errors.New("missing database identity")
	}
	return x.Server + "/" + x.Name, nil
}
func engines(tx *gorm.DB, withAudit bool) error {
	tables := []string{"logan_task", "logan_log_detail", "logan_processing_job"}
	if withAudit {
		tables = append(tables, auditTable)
	}
	for _, table := range tables {
		var engine string
		if e := tx.Raw("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&engine).Error; e != nil {
			return e
		}
		if !strings.EqualFold(engine, "InnoDB") {
			return fmt.Errorf("%s requires InnoDB and explicit migrations", table)
		}
	}
	return nil
}
func (t *Tool) load(tx *gorm.DB, id int64, lock bool) (snapshot, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	task, e := boundedRows(tx, 1, t.limits.MaxSnapshot, "SELECT * FROM logan_task WHERE id=?"+suffix, id)
	if e != nil {
		return snapshot{}, e
	}
	if len(task) != 1 {
		return snapshot{}, gorm.ErrRecordNotFound
	}
	encoded, _ := json.Marshal(task[0])
	remaining := t.limits.MaxSnapshot - int64(len(encoded))
	if remaining <= 0 {
		return snapshot{}, protocol.ErrLimit
	}
	details, e := boundedRows(tx, t.limits.MaxRecords, remaining, "SELECT * FROM logan_log_detail WHERE task_id=? ORDER BY id LIMIT "+strconv.Itoa(t.limits.MaxRecords+1)+suffix, id)
	if e != nil {
		return snapshot{}, e
	}
	result := snapshot{task[0], details}
	encoded, e = json.Marshal(result)
	if e != nil {
		return snapshot{}, e
	}
	if int64(len(encoded)) > t.limits.MaxSnapshot {
		return snapshot{}, protocol.ErrLimit
	}
	return result, nil
}
func eligible(tx *gorm.DB, id int64, lock bool) error {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	j, e := boundedRows(tx, 1, 1<<20, "SELECT * FROM logan_processing_job WHERE kind='native' AND task_id=?"+suffix, id)
	if e != nil {
		return e
	}
	if len(j) > 0 && str(j[0]["state"]) != "done" && str(j[0]["state"]) != "failed" {
		return errors.New("native job is not terminal")
	}
	return nil
}
func prefix(existing []map[string]any, parsed []protocol.NativeRecord) error {
	if len(existing) > len(parsed) {
		return errors.New("existing details exceed parsed records")
	}
	for i, r := range parsed {
		if r.Content == nil {
			return errors.New("null content cannot be projected losslessly")
		}
		if i < len(existing) {
			d := existing[i]
			if str(d["content"]) != *r.Content || number(d["log_type"]) != int64(r.LogType) || number(d["log_time"]) != r.LogTime {
				return fmt.Errorf("existing detail %d is not an exact prefix", i)
			}
		}
	}
	return nil
}
func (t *Tool) parse(ctx context.Context, name string) (records []protocol.NativeRecord, digest string, compressed, plain int64, err error) {
	if t.objects == nil {
		return nil, "", 0, 0, errors.New("object reader required")
	}
	src, e := t.objects.Get(ctx, name)
	if e != nil {
		err = e
		return
	}
	var closeOnce sync.Once
	closeBody := func() { closeOnce.Do(func() { _ = src.Close() }) }
	stopClose := context.AfterFunc(ctx, closeBody)
	defer func() { stopClose(); closeBody() }()
	c, e := os.CreateTemp(t.limits.TempDir, "native-reparse-compressed-*")
	if e != nil {
		err = e
		return
	}
	defer os.Remove(c.Name())
	defer c.Close()
	h := sha256.New()
	compressed, e = io.Copy(io.MultiWriter(c, h), io.LimitReader(src, t.limits.MaxCompressed+1))
	if e != nil {
		err = e
		return
	}
	if compressed > t.limits.MaxCompressed {
		err = protocol.ErrLimit
		return
	}
	digest = hex.EncodeToString(h.Sum(nil))
	if _, e = c.Seek(0, 0); e != nil {
		err = e
		return
	}
	p, e := os.CreateTemp(t.limits.TempDir, "native-reparse-plain-*")
	if e != nil {
		err = e
		return
	}
	defer os.Remove(p.Name())
	defer p.Close()
	if e = protocol.DecompressStrict(ctx, c, p, t.limits.MaxPlain); e != nil {
		err = e
		return
	}
	info, e := p.Stat()
	if e != nil {
		err = e
		return
	}
	plain = info.Size()
	if _, e = p.Seek(0, 0); e != nil {
		err = e
		return
	}
	err = protocol.ParseNativeRecords(ctx, p, t.limits.MaxLine, t.limits.MaxRecords, func(r protocol.NativeRecord) error { records = append(records, r); return nil })
	return
}
func (t *Tool) DryRun(ctx context.Context, id int64) (Plan, error) {
	var p Plan
	if id <= 0 {
		return p, errors.New("positive task id required")
	}
	db := t.db.WithContext(ctx)
	if e := engines(db, false); e != nil {
		return p, e
	}
	ident, e := identity(db)
	if e != nil {
		return p, e
	}
	var initial snapshot
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := eligible(tx, id, false); e != nil {
			return e
		}
		var e error
		initial, e = t.load(tx, id, false)
		return e
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return p, e
	}
	if number(initial.Task["status"]) != 1 {
		return p, errors.New("task must be completed (status 1)")
	}
	name := str(initial.Task["log_file_name"])
	if name == "" {
		return p, errors.New("task has no object")
	}
	r, d, c, n, e := t.parse(ctx, name)
	if e != nil {
		return p, e
	}
	if e = prefix(initial.Details, r); e != nil {
		return p, e
	}
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := eligible(tx, id, false); e != nil {
			return e
		}
		s, e := t.load(tx, id, false)
		if e == nil && hash(s) != hash(initial) {
			return errors.New("task changed while parsing")
		}
		return e
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return p, e
	}
	token := make([]byte, 16)
	if _, e = rand.Read(token); e != nil {
		return p, e
	}
	p = Plan{OperationID: hex.EncodeToString(token), TaskID: id, ObjectName: name, ObjectSHA256: d, ExpectedDigest: hash(r), BeforeHash: hash(initial), DatabaseIdentity: ident, SchemaVersion: SchemaVersion, ToolVersion: ToolVersion, ExistingRecords: len(initial.Details), ParsedRecords: len(r), AppendRecords: len(r) - len(initial.Details), CompressedBytes: c, PlainBytes: n}
	p.Fingerprint = fingerprint(p)
	return p, nil
}
func valid(p Plan) error {
	b, e := hex.DecodeString(p.OperationID)
	if e != nil || len(b) != 16 || p.TaskID <= 0 || p.ToolVersion != ToolVersion || p.SchemaVersion != SchemaVersion || p.Fingerprint != fingerprint(p) {
		return errors.New("invalid or modified plan")
	}
	return nil
}
func (t *Tool) Apply(ctx context.Context, p Plan) (Result, error) {
	result := Result{OperationID: p.OperationID, TaskID: p.TaskID, State: "applied", AppendedRecords: p.AppendRecords}
	if e := valid(p); e != nil {
		return result, e
	}
	db := t.db.WithContext(ctx)
	if e := engines(db, true); e != nil {
		return result, e
	}
	if e := t.authorize(db, p.DatabaseIdentity, p.OperationID, p.Fingerprint); e != nil {
		return result, e
	}
	// Retry is checked before object retrieval, so an already applied operation remains idempotent if OSS is unavailable.
	var prior audit
	q := db.Select("operation_id").Where("operation_id=?", p.OperationID).Find(&prior)
	if q.Error != nil {
		return result, q.Error
	}
	if q.RowsAffected > 0 {
		return t.retry(ctx, p)
	}
	r, d, c, n, e := t.parse(ctx, p.ObjectName)
	if e != nil {
		return result, e
	}
	if d != p.ObjectSHA256 || hash(r) != p.ExpectedDigest || c != p.CompressedBytes || n != p.PlainBytes || len(r) != p.ParsedRecords {
		return result, errors.New("object no longer matches plan")
	}
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := t.authorize(tx, p.DatabaseIdentity, p.OperationID, p.Fingerprint); e != nil {
			return e
		}
		a := audit{OperationID: p.OperationID, TaskID: p.TaskID, Fingerprint: p.Fingerprint, DatabaseIdentity: p.DatabaseIdentity, BeforeHash: p.BeforeHash, State: "applying"}
		if e := tx.Create(&a).Error; e != nil {
			return e
		}
		if e := eligible(tx, p.TaskID, true); e != nil {
			return e
		}
		s, e := t.load(tx, p.TaskID, true)
		if e != nil {
			return e
		}
		if hash(s) != p.BeforeHash || number(s.Task["status"]) != 1 {
			return errors.New("stale plan")
		}
		if len(s.Details) != p.ExistingRecords || p.AppendRecords != len(r)-len(s.Details) {
			return errors.New("plan counts mismatch")
		}
		if e = prefix(s.Details, r); e != nil {
			return e
		}
		ids := []int64{}
		for _, rec := range r[len(s.Details):] {
			q := tx.Exec("INSERT INTO logan_log_detail(task_id,log_type,content,log_time) VALUES (?,?,?,?)", p.TaskID, rec.LogType, *rec.Content, rec.LogTime)
			if q.Error != nil {
				return q.Error
			}
			var id int64
			if e = tx.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; e != nil {
				return e
			}
			ids = append(ids, id)
		}
		after, e := t.load(tx, p.TaskID, true)
		if e != nil {
			return e
		}
		if e = published(s, after, r); e != nil {
			return e
		}
		if e = appendIDsMatch(after, len(s.Details), ids); e != nil {
			return e
		}
		beforeJSON, _ := json.Marshal(s)
		afterJSON, _ := json.Marshal(after)
		idJSON, _ := json.Marshal(ids)
		return tx.Model(&audit{}).Where("operation_id=?", p.OperationID).Updates(map[string]any{"before_snapshot": string(beforeJSON), "after_snapshot": string(afterJSON), "after_hash": hash(after), "append_ids": string(idJSON), "state": "applied"}).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil { // A concurrent identical invocation may have won the unique operation key.
		var a audit
		if db.Select("operation_id").Where("operation_id=?", p.OperationID).Find(&a).Error == nil && a.OperationID != "" {
			return t.retry(ctx, p)
		}
	}
	return result, e
}
func (t *Tool) retry(ctx context.Context, p Plan) (Result, error) {
	result := Result{OperationID: p.OperationID, TaskID: p.TaskID, State: "applied", AppendedRecords: p.AppendRecords, Noop: true}
	e := t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var a audit
		q := tx.Raw(t.auditSelect()+" WHERE operation_id=? FOR UPDATE", p.OperationID).Scan(&a)
		if q.Error != nil {
			return q.Error
		}
		if e := t.authorize(tx, p.DatabaseIdentity, p.OperationID, p.Fingerprint); e != nil {
			return e
		}
		if a.Fingerprint != p.Fingerprint || a.State != "applied" || a.DatabaseIdentity != p.DatabaseIdentity {
			return errors.New("operation cannot be reapplied")
		}
		if e := eligible(tx, p.TaskID, true); e != nil {
			return e
		}
		s, e := t.load(tx, p.TaskID, true)
		if e != nil {
			return e
		}
		if hash(s) != a.AfterHash {
			return errors.New("applied task has changed")
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return result, e
}
func (t *Tool) Rollback(ctx context.Context, operationID string) (Result, error) {
	result := Result{OperationID: operationID, State: "rolled_back"}
	db := t.db.WithContext(ctx)
	if e := engines(db, true); e != nil {
		return result, e
	}
	_, e := identity(db)
	if e != nil {
		return result, e
	}
	e = db.Transaction(func(tx *gorm.DB) error {
		var a audit
		q := tx.Raw(t.auditSelect()+" WHERE operation_id=? FOR UPDATE", operationID).Scan(&a)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		result.TaskID = a.TaskID
		if e := t.authorize(tx, a.DatabaseIdentity, a.OperationID, a.Fingerprint); e != nil {
			return e
		}
		if e := eligible(tx, a.TaskID, true); e != nil {
			return e
		}
		s, e := t.load(tx, a.TaskID, true)
		if e != nil {
			return e
		}
		if a.State == "rolled_back" {
			if hash(s) != a.BeforeHash {
				return errors.New("rolled back task has changed")
			}
			result.Noop = true
			return nil
		}
		if a.State != "applied" || hash(s) != a.AfterHash || number(s.Task["status"]) != 1 {
			return errors.New("rollback requires unchanged applied snapshot")
		}
		if int64(len(a.AppendIDs)) > t.limits.MaxSnapshot {
			return protocol.ErrLimit
		}
		var ids []int64
		if e = json.Unmarshal([]byte(a.AppendIDs), &ids); e != nil {
			return e
		}
		if len(ids) > t.limits.MaxRecords {
			return protocol.ErrLimit
		}
		result.AppendedRecords = len(ids)
		if len(ids) > 0 {
			q := tx.Exec("DELETE FROM logan_log_detail WHERE task_id=? AND id IN ?", a.TaskID, ids)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != int64(len(ids)) {
				return errors.New("appended rows changed")
			}
		}
		restored, e := t.load(tx, a.TaskID, true)
		if e != nil {
			return e
		}
		if hash(restored) != a.BeforeHash {
			return errors.New("rollback snapshot mismatch")
		}
		return tx.Model(&audit{}).Where("operation_id=?", operationID).Update("state", "rolled_back").Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return result, e
}

// Archived raw snapshots are write-only evidence; operational reads never fetch them.
func (t *Tool) auditSelect() string {
	return "SELECT operation_id,task_id,fingerprint,database_identity,before_hash,after_hash,state,LEFT(append_ids," + strconv.FormatInt(t.limits.MaxSnapshot+1, 10) + ") AS append_ids FROM " + auditTable
}

// Verify database conversion and triggers did not alter the desired publication.
func published(before, after snapshot, records []protocol.NativeRecord) error {
	if len(after.Details) != len(records) {
		return errors.New("published detail count mismatch")
	}
	if hash(before.Task) != hash(after.Task) {
		return errors.New("publication changed task fields")
	}
	if len(after.Details) < len(before.Details) || hash(before.Details) != hash(after.Details[:len(before.Details)]) {
		return errors.New("publication changed existing detail fields")
	}
	if e := prefix(after.Details, records); e != nil {
		return fmt.Errorf("publication differs from parsed records: %w", e)
	}
	return nil
}

// The rollback audit must identify exactly the rows observed after publication.
func appendIDsMatch(after snapshot, existing int, ids []int64) error {
	if existing < 0 || existing > len(after.Details) || len(ids) != len(after.Details)-existing {
		return errors.New("appended audit ID count mismatch")
	}
	seen := make(map[int64]bool, len(ids))
	for i, id := range ids {
		if id <= 0 || seen[id] || number(after.Details[existing+i]["id"]) != id {
			return errors.New("appended audit IDs differ from published rows")
		}
		seen[id] = true
	}
	return nil
}
