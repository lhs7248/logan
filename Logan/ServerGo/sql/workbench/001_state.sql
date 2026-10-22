-- Explicit, additive migration for the optional independent workbench runtime.
-- An operator must review and execute this against an isolated database first.
-- The executable never runs this migration or modifies any legacy table.
CREATE TABLE workbench_state (
    namespace VARCHAR(128) NOT NULL PRIMARY KEY,
    revision BIGINT NOT NULL,
    payload LONGBLOB NOT NULL
) ENGINE=InnoDB;
