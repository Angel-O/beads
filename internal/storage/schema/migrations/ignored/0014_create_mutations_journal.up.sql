-- Materialize the clone-local mutations journal table on clones that never ran
-- main migration 0056. A fresh clone arrives with the schema_migrations cursor
-- at-latest (0056 already recorded) but WITHOUT the table, exactly like the
-- leases/wisp/local-state tables from ignored/0001 and ignored/0012.
--
-- bd_mutations_journal is dolt_ignored (seeded by MigrateUp's doltIgnorePatterns
-- before this migration runs): operational, never versioned or federated, so its
-- AUTO_INCREMENT seq stays monotonic and collision-free with no merge conflict.
-- Same __temp__ + conditional RENAME pattern: create only when absent, never
-- touch an existing table. See issueops.RecordMutationInTx for the writer.
DROP TABLE IF EXISTS __temp__bd_mutations_journal;
CREATE TABLE __temp__bd_mutations_journal (
    seq BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    ts DATETIME NOT NULL,
    op VARCHAR(32) NOT NULL,
    issue_id VARCHAR(255) NOT NULL,
    issue_json LONGTEXT,
    dep_json TEXT,
    INDEX idx_bd_mutations_journal_issue (issue_id)
);
SET @exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bd_mutations_journal');
SET @sql = IF(@exists = 0, 'RENAME TABLE __temp__bd_mutations_journal TO bd_mutations_journal', 'DROP TABLE __temp__bd_mutations_journal');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
