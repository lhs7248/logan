package reparse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const IdentityVersion = "native-reparse-identity-v1"
const identityTable = "logan_native_reparse_identity_authorization"
const maxWitnessBytes = 60 << 10

type IdentityWitness struct {
	Version        string `json:"version"`
	Plan           Plan   `json:"plan"`
	SourceIdentity string `json:"source_identity"`
	SnapshotHash   string `json:"snapshot_hash"`
	AuditHash      string `json:"audit_hash"`
	State          string `json:"state"`
	SourceGTID     string `json:"source_gtid"`
	WitnessID      string `json:"witness_id"`
	Digest         string `json:"digest"`
}
type IdentityMigrationResult struct {
	OperationID    string `json:"operation_id"`
	WitnessID      string `json:"witness_id"`
	SourceIdentity string `json:"source_identity"`
	TargetIdentity string `json:"target_identity"`
	DryRun         bool   `json:"dry_run"`
	Noop           bool   `json:"noop"`
	Digest         string `json:"digest"`
}
type authorization struct {
	OperationID         string
	TargetIdentity      string
	Fingerprint         string
	SourceIdentity      string
	WitnessID           string
	WitnessDigest       string
	WitnessJSON         string
	AuthorizationDigest string
}

func (authorization) TableName() string          { return identityTable }
func authorizationDigest(a authorization) string { a.AuthorizationDigest = ""; return hash(a) }
func witnessDigest(w IdentityWitness) string     { w.Digest = ""; return hash(w) }
func hexLength(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(s) == n && len(b)*2 == n && strings.ToLower(s) == s
}
func identityParts(s string) (string, string, error) {
	p := strings.Split(s, "/")
	if len(p) != 2 || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(p[0]) || !regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`).MatchString(p[1]) {
		return "", "", errors.New("invalid identity")
	}
	return p[0], p[1], nil
}
func validateWitness(w IdentityWitness) error {
	b, e := json.Marshal(w)
	if e != nil || len(b) > maxWitnessBytes || w.Version != IdentityVersion || !hexLength(w.WitnessID, 32) || !hexLength(w.Digest, 64) || w.Digest != witnessDigest(w) || w.SourceIdentity != w.Plan.DatabaseIdentity || !hexLength(w.SnapshotHash, 64) || w.SourceGTID == "" || len(w.SourceGTID) > 32768 {
		return errors.New("invalid identity witness")
	}
	if w.State != "pending" && w.State != "applied" && w.State != "rolled_back" {
		return errors.New("invalid witness state")
	}
	if (w.State == "pending" && w.AuditHash != "") || (w.State != "pending" && !hexLength(w.AuditHash, 64)) {
		return errors.New("invalid audit witness")
	}
	if _, _, e := identityParts(w.SourceIdentity); e != nil {
		return e
	}
	p := w.Plan
	if !hexLength(p.OperationID, 32) || !hexLength(p.BeforeHash, 64) || !hexLength(p.ObjectSHA256, 64) || !hexLength(p.ExpectedDigest, 64) || !hexLength(p.Fingerprint, 64) || p.ObjectName == "" || len(p.ObjectName) > 1024 || p.ExistingRecords < 0 || p.ParsedRecords < p.ExistingRecords || p.AppendRecords != p.ParsedRecords-p.ExistingRecords || p.CompressedBytes <= 0 || p.PlainBytes <= 0 {
		return errors.New("invalid witness plan metadata")
	}
	return valid(w.Plan)
}

// proofAudit contains only fixed metadata and SQL-side hashes, never archived log bodies.
type proofAudit struct {
	OperationID                                                                                                      string
	TaskID                                                                                                           int64
	Fingerprint, DatabaseIdentity, BeforeHash, AfterHash, State, BeforeSHA, AfterSHA, AppendSHA, AppendIDs, AfterIDs string
}

func (t *Tool) currentProof(tx *gorm.DB, p Plan, lock bool) (string, string, string, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var a proofAudit
	budget := strconv.FormatInt(t.limits.MaxSnapshot, 10)
	guard := func(col, expr string) string {
		return "CASE WHEN OCTET_LENGTH(" + col + ") <= " + budget + " THEN " + expr + " ELSE NULL END"
	}
	q := tx.Raw("SELECT operation_id,task_id,fingerprint,database_identity,before_hash,after_hash,state,"+guard("before_snapshot", "SHA2(before_snapshot,256)")+" AS before_sha,"+guard("after_snapshot", "SHA2(after_snapshot,256)")+" AS after_sha,"+guard("append_ids", "SHA2(append_ids,256)")+" AS append_sha,"+guard("append_ids", "append_ids")+" AS append_ids,"+guard("after_snapshot", "LEFT(COALESCE(JSON_EXTRACT(after_snapshot,'$.Details[*].id'),JSON_ARRAY()),"+strconv.FormatInt(t.limits.MaxSnapshot+1, 10)+")")+" AS after_ids FROM "+auditTable+" WHERE operation_id=?"+suffix, p.OperationID).Scan(&a)
	if q.Error != nil {
		return "", "", "", q.Error
	}
	if e := eligible(tx, p.TaskID, lock); e != nil {
		return "", "", "", e
	}
	s, e := t.load(tx, p.TaskID, lock)
	if e != nil {
		return "", "", "", e
	}
	sh := hash(s)
	if number(s.Task["status"]) != 1 || str(s.Task["log_file_name"]) != p.ObjectName {
		return "", "", "", errors.New("task is not completed plan task")
	}
	if q.RowsAffected == 0 {
		if sh != p.BeforeHash || len(s.Details) != p.ExistingRecords {
			return "", "", "", errors.New("pending snapshot changed")
		}
		return "pending", sh, "", nil
	}
	if a.OperationID != p.OperationID || a.TaskID != p.TaskID || a.Fingerprint != p.Fingerprint || a.DatabaseIdentity != p.DatabaseIdentity || a.BeforeHash != p.BeforeHash || a.BeforeSHA != a.BeforeHash || a.AfterSHA != a.AfterHash || !hexLength(a.AppendSHA, 64) {
		return "", "", "", errors.New("audit archive or identity mismatch")
	}
	var ids []int64
	if int64(len(a.AppendIDs)) > t.limits.MaxSnapshot || json.Unmarshal([]byte(a.AppendIDs), &ids) != nil || len(ids) != p.AppendRecords || len(ids) > t.limits.MaxRecords {
		return "", "", "", errors.New("invalid archived append IDs")
	}
	sum := sha256.Sum256([]byte(a.AppendIDs))
	if hex.EncodeToString(sum[:]) != a.AppendSHA {
		return "", "", "", errors.New("append archive hash mismatch")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return "", "", "", errors.New("invalid archived append IDs")
		}
		seen[id] = true
	}
	if a.State == "applied" {
		if len(s.Details) != p.ParsedRecords {
			return "", "", "", errors.New("applied count mismatch")
		}
		if e := appendIDsMatch(s, p.ExistingRecords, ids); e != nil {
			return "", "", "", e
		}
	}
	if a.State == "rolled_back" {
		var archived []int64
		if json.Unmarshal([]byte(a.AfterIDs), &archived) != nil || len(archived) != p.ParsedRecords {
			return "", "", "", errors.New("invalid archived IDs")
		}
		for i, id := range archived {
			if i < p.ExistingRecords {
				if i >= len(s.Details) || number(s.Details[i]["id"]) != id {
					return "", "", "", errors.New("archived prefix mismatch")
				}
			} else if id != ids[i-p.ExistingRecords] {
				return "", "", "", errors.New("archived suffix mismatch")
			}
		}
	}
	if a.State == "rolled_back" {
		if len(s.Details) != p.ExistingRecords {
			return "", "", "", errors.New("restored count mismatch")
		}
		for _, d := range s.Details {
			if seen[number(d["id"])] {
				return "", "", "", errors.New("rollback retained append ID")
			}
		}
	}
	expected := a.AfterHash
	if a.State == "rolled_back" {
		expected = a.BeforeHash
	} else if a.State != "applied" {
		return "", "", "", errors.New("audit not terminal")
	}
	if sh != expected {
		return "", "", "", errors.New("audit business snapshot mismatch")
	}
	state := a.State
	a.State = "" // State advances legally; archive identity and content must remain immutable.
	return state, sh, hash(a), nil
}
func (t *Tool) ExportIdentity(ctx context.Context, p Plan) (IdentityWitness, error) {
	var w IdentityWitness
	if e := valid(p); e != nil {
		return w, e
	}
	if p.ParsedRecords > t.limits.MaxRecords || p.CompressedBytes > t.limits.MaxCompressed || p.PlainBytes > t.limits.MaxPlain {
		return w, errors.New("plan exceeds resource limits")
	}
	e := t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := engines(tx, true); e != nil {
			return e
		}
		var beforeGTID string
		if e := tx.Raw("SELECT @@GLOBAL.gtid_executed").Scan(&beforeGTID).Error; e != nil {
			return e
		}
		id, e := identity(tx)
		if e != nil {
			return e
		}
		if id != p.DatabaseIdentity {
			return errors.New("source identity mismatch")
		}
		state, sh, ah, e := t.currentProof(tx, p, false)
		if e != nil {
			return e
		}
		var gtid string
		if e = tx.Raw("SELECT @@GLOBAL.gtid_executed").Scan(&gtid).Error; e != nil {
			return e
		}
		if beforeGTID != gtid {
			return errors.New("source GTID changed during export")
		}
		token := make([]byte, 16)
		if _, e = rand.Read(token); e != nil {
			return e
		}
		w = IdentityWitness{Version: IdentityVersion, Plan: p, SourceIdentity: id, SnapshotHash: sh, AuditHash: ah, State: state, SourceGTID: gtid, WitnessID: hex.EncodeToString(token)}
		w.Digest = witnessDigest(w)
		return validateWitness(w)
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	return w, e
}
func identityEngine(tx *gorm.DB) error {
	var e string
	if err := tx.Raw("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", identityTable).Scan(&e).Error; err != nil {
		return err
	}
	if !strings.EqualFold(e, "InnoDB") {
		return errors.New("identity authorization requires explicit InnoDB migration")
	}
	return nil
}
func loadAuthorization(tx *gorm.DB, op, target string, lock bool) (authorization, bool, error) {
	var a authorization
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	q := tx.Raw("SELECT operation_id,target_identity,fingerprint,source_identity,witness_id,witness_digest,LEFT(witness_json,61441) AS witness_json,authorization_digest FROM "+identityTable+" WHERE operation_id=? AND target_identity=?"+suffix, op, target).Scan(&a)
	if q.Error != nil {
		return a, false, q.Error
	}
	if q.RowsAffected == 0 {
		return a, false, nil
	}
	var w IdentityWitness
	if len(a.WitnessJSON) > maxWitnessBytes || json.Unmarshal([]byte(a.WitnessJSON), &w) != nil || validateWitness(w) != nil || a.AuthorizationDigest != authorizationDigest(a) || a.OperationID != w.Plan.OperationID || a.Fingerprint != w.Plan.Fingerprint || a.SourceIdentity != w.SourceIdentity || a.WitnessID != w.WitnessID || a.WitnessDigest != w.Digest {
		return a, false, errors.New("invalid identity authorization")
	}
	return a, true, nil
}
func (t *Tool) authorize(tx *gorm.DB, source, op, fp string) error {
	actual, e := identity(tx)
	if e != nil {
		return e
	}
	if actual == source {
		return nil
	}
	if e = identityEngine(tx); e != nil {
		return e
	}
	a, ok, e := loadAuthorization(tx, op, actual, false)
	if e != nil {
		return e
	}
	_, sn, se := identityParts(source)
	_, tn, te := identityParts(actual)
	if !ok || se != nil || te != nil || sn != tn || a.TargetIdentity != actual || a.SourceIdentity != source || a.Fingerprint != fp {
		return errors.New("database identity mismatch without exact authorization")
	}
	var w IdentityWitness
	if json.Unmarshal([]byte(a.WitnessJSON), &w) != nil {
		return errors.New("invalid identity authorization")
	}
	state, _, ah, e := t.currentProof(tx, w.Plan, false)
	if e != nil {
		return e
	}
	if (w.State == "applied" && state == "pending") || (w.State == "rolled_back" && state != "rolled_back") || (w.AuditHash != "" && ah != w.AuditHash) {
		return errors.New("authorized audit history changed")
	}
	return nil
}
func (t *Tool) MigrateIdentity(ctx context.Context, w IdentityWitness, trustedDigest, expectedSource, expectedTarget string, dryRun bool) (IdentityMigrationResult, error) {
	r := IdentityMigrationResult{OperationID: w.Plan.OperationID, WitnessID: w.WitnessID, SourceIdentity: w.SourceIdentity, TargetIdentity: expectedTarget, DryRun: dryRun, Digest: w.Digest}
	if e := validateWitness(w); e != nil {
		return r, e
	}
	if w.Plan.ParsedRecords > t.limits.MaxRecords || w.Plan.CompressedBytes > t.limits.MaxCompressed || w.Plan.PlainBytes > t.limits.MaxPlain {
		return r, errors.New("plan exceeds resource limits")
	}
	su, sn, se := identityParts(expectedSource)
	tu, tn, te := identityParts(expectedTarget)
	if !hexLength(trustedDigest, 64) || trustedDigest != w.Digest || expectedSource != w.SourceIdentity || se != nil || te != nil || su == tu || sn != tn {
		return r, errors.New("identity trust or destination mismatch")
	}
	e := t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := engines(tx, true); e != nil {
			return e
		}
		if e := identityEngine(tx); e != nil {
			return e
		}
		actual, e := identity(tx)
		if e != nil {
			return e
		}
		if actual != expectedTarget {
			return errors.New("actual target identity mismatch")
		}
		var subset int
		if e = tx.Raw("SELECT GTID_SUBSET(?, @@GLOBAL.gtid_executed)", w.SourceGTID).Scan(&subset).Error; e != nil {
			return e
		}
		if subset != 1 {
			return errors.New("target has not executed source GTID")
		}
		a, exists, e := loadAuthorization(tx, w.Plan.OperationID, actual, !dryRun)
		if e != nil {
			return e
		}
		state, sh, ah, e := t.currentProof(tx, w.Plan, !dryRun)
		if e != nil {
			return e
		}
		if exists {
			if a.WitnessDigest != w.Digest || a.SourceIdentity != w.SourceIdentity || a.Fingerprint != w.Plan.Fingerprint {
				return errors.New("conflicting identity authorization")
			}
			if (w.State == "applied" && state == "pending") || (w.State == "rolled_back" && state != "rolled_back") || (w.AuditHash != "" && ah != w.AuditHash) {
				return errors.New("authorized audit history changed")
			}
			r.Noop = true
			return nil
		}
		if state != w.State || sh != w.SnapshotHash || ah != w.AuditHash {
			return errors.New("target proof differs from source witness")
		}
		if dryRun {
			return nil
		}
		raw, _ := json.Marshal(w)
		a = authorization{OperationID: w.Plan.OperationID, TargetIdentity: actual, Fingerprint: w.Plan.Fingerprint, SourceIdentity: w.SourceIdentity, WitnessID: w.WitnessID, WitnessDigest: w.Digest, WitnessJSON: string(raw)}
		a.AuthorizationDigest = authorizationDigest(a)
		if e := tx.Create(&a).Error; e != nil {
			return e
		}
		published, found, e := loadAuthorization(tx, w.Plan.OperationID, actual, true)
		if e != nil {
			return e
		}
		if !found || published != a {
			return errors.New("published authorization differs from requested grant")
		}
		afterState, afterSnapshot, afterAudit, e := t.currentProof(tx, w.Plan, true)
		if e != nil {
			return e
		}
		if afterState != state || afterSnapshot != sh || afterAudit != ah {
			return errors.New("identity grant publication changed business or audit")
		}
		return nil
	}, &sql.TxOptions{ReadOnly: dryRun, Isolation: sql.LevelRepeatableRead})
	return r, e
}
