-- Explicit isolated-operation migration only. No startup auto-DDL.
-- Append-only per-operation grants; never rewrite the original operation identity.
CREATE TABLE IF NOT EXISTS logan_native_reparse_identity_authorization (
 operation_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 target_identity VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_identity VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 witness_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 witness_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 witness_json MEDIUMTEXT NOT NULL,
 authorization_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY (operation_id,target_identity)
) ENGINE=InnoDB;
