-- Explicit isolated-operation migration. Never applied automatically by the server/tool.
-- Preserve this audit before removing tooling. Rollback data through the tool before
-- DROP TABLE logan_native_reparse_operation; dropping audit loses rollback evidence.
CREATE TABLE IF NOT EXISTS logan_native_reparse_operation (
 operation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT NOT NULL,
 fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 database_identity VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 before_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 after_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 before_snapshot LONGTEXT NOT NULL,
 after_snapshot LONGTEXT NOT NULL,
 append_ids LONGTEXT NOT NULL,
 state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY (operation_id),
 KEY reparse_task (task_id)
) ENGINE=InnoDB;
