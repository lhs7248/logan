-- Apply explicitly before enabling Web processing. MySQL 5.7 / 8.0.
-- Rollback (after workers stop): DROP TABLE logan_web_upload_lock;
-- DROP TABLE logan_processing_job; this removes queued job state only.
CREATE TABLE IF NOT EXISTS logan_processing_job (
 kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT NOT NULL,
 source_revision BIGINT NOT NULL DEFAULT 1,
 source_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
 owner CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 lease_until BIGINT NOT NULL DEFAULT 0,
 fence BIGINT NOT NULL DEFAULT 0,
 attempts INT NOT NULL DEFAULT 0,
 next_run_at BIGINT NOT NULL DEFAULT 0,
 last_error_code VARCHAR(64) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 PRIMARY KEY (kind,task_id),
 KEY job_pending (kind,state,next_run_at),
 KEY job_expired (kind,state,lease_until)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS logan_web_upload_lock (
 identity_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY (identity_key)
) ENGINE=InnoDB;
-- Source and detail tables must also use InnoDB for atomic publication.
-- Upload identity rows intentionally persist; do not remove while uploads run.
