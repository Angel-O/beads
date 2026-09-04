-- Migration 0067: add durable scope identity, active-state, and membership.
--
-- Scope identity is caller-supplied and created_on has no ON UPDATE clause.
-- Their immutability is part of the scope API contract owned by the follow-on
-- storage work; this migration only establishes their durable shape. Existing
-- issues are intentionally not copied into scope_members.
CREATE TABLE IF NOT EXISTS scopes (
    id CHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    created_on DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_scopes_normalized_created_on (normalized_name, created_on)
);

CREATE TABLE IF NOT EXISTS scope_state (
    singleton_id TINYINT NOT NULL PRIMARY KEY,
    active_scope_id CHAR(36) NULL,
    CONSTRAINT ck_scope_state_singleton CHECK (singleton_id = 1),
    CONSTRAINT fk_scope_state_active_scope FOREIGN KEY (active_scope_id)
        REFERENCES scopes(id) ON DELETE SET NULL
);
INSERT IGNORE INTO scope_state (singleton_id, active_scope_id) VALUES (1, NULL);

CREATE TABLE IF NOT EXISTS scope_members (
    issue_id VARCHAR(255) NOT NULL PRIMARY KEY,
    scope_id CHAR(36) NOT NULL,
    CONSTRAINT fk_scope_members_issue FOREIGN KEY (issue_id)
        REFERENCES issues(id) ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT fk_scope_members_scope FOREIGN KEY (scope_id)
        REFERENCES scopes(id) ON DELETE CASCADE ON UPDATE CASCADE,
    INDEX idx_scope_members_scope (scope_id)
);

-- The cursor can regress without its DDL being rolled back; keep the later
-- keyset index replayable while using a direct CREATE INDEX on the CLI path.
SET @needs_scope_issue_index = (
    SELECT IF(COUNT(*) = 0, 1, 0)
    FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'issues'
      AND INDEX_NAME = 'idx_issues_updated_at_id'
);
SET @sql = IF(@needs_scope_issue_index = 1,
    'CREATE INDEX idx_issues_updated_at_id ON issues (updated_at, id)',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
