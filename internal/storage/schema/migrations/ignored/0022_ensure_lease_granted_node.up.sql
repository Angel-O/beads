-- Repair historical ignored-migration ordinal collision: some clones recorded
-- a different v16 body before granted_node occupied that ordinal. Their
-- ignored cursor can already be beyond v16 while clone-local leases lacks the
-- column. This next-free migration is therefore guarded and idempotent.
SET @needs_add = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'leases') > 0
    AND
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'leases'
          AND COLUMN_NAME = 'granted_node') = 0,
    1, 0
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE leases ADD COLUMN granted_node VARCHAR(255) NOT NULL DEFAULT ''''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
