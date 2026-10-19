-- Apply explicitly after 001_web_processing.sql, before enabling maintenance.
-- MySQL 5.7 / 8.0. No original table definitions are changed.
-- Source/detail/shared-job tables must also use InnoDB (checked at startup).
CREATE TABLE IF NOT EXISTS logan_maintenance_lease (
 name VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 owner VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 fence BIGINT NOT NULL DEFAULT 0,
 lease_until BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (name)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS logan_managed_object (
 scope CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 name VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 owner VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 fence BIGINT NOT NULL DEFAULT 0,
 lease_until BIGINT NOT NULL DEFAULT 0,
 attempts INT NOT NULL DEFAULT 0,
 next_run_at BIGINT NOT NULL DEFAULT 0,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 last_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 PRIMARY KEY (scope,key_hash),
 KEY object_pending (scope,state,next_run_at),
 KEY object_expired (scope,state,lease_until),
 KEY object_orphan (scope,state,updated_at)
) ENGINE=InnoDB;
-- Tombstones intentionally persist to prevent object-name reuse after deletion.
-- Rollback only after stopping upload/maintenance workers and preserving the
-- managed-object registry: DROP TABLE logan_managed_object;
-- DROP TABLE logan_maintenance_lease; queued deletion and tombstones are lost.

-- A database serves exactly one configured OSS namespace. The singleton primary
-- key makes concurrent first startup from conflicting namespaces choose one.
-- Never reset/rebind this row while source tasks, intents or objects survive.
CREATE TABLE IF NOT EXISTS logan_storage_namespace (
 id TINYINT NOT NULL,
 scope CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY (id)
) ENGINE=InnoDB;
-- Rollback also requires stopping all users and preserving namespace ownership:
-- DROP TABLE logan_storage_namespace; removing it must not authorize rebinding
-- an existing source database to a different bucket or prefix.
