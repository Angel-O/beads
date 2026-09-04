-- Reverse 0067: remove scope state and memberships before their referenced
-- scope identity table. The issue index is restored only by a later forward
-- migration if it is still needed.
DROP TABLE IF EXISTS scope_members;
DROP TABLE IF EXISTS scope_state;
DROP TABLE IF EXISTS scopes;

SET @has_scope_issue_index = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'issues'
      AND INDEX_NAME = 'idx_issues_updated_at_id'
);
SET @sql = IF(@has_scope_issue_index > 0,
    'DROP INDEX idx_issues_updated_at_id ON issues',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
