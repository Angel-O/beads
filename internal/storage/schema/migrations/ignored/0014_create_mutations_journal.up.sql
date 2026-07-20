-- Materialize the clone-local mutations journal table on clones that never ran
-- main migration 0056. A fresh clone arrives with the schema_migrations cursor
-- at-latest (0056 already recorded) but WITHOUT the table, exactly like the
-- leases/wisp/local-state tables from ignored/0001 and ignored/0012.
--
-- bd_mutations_journal is dolt_ignored (seeded by MigrateUp's doltIgnorePatterns
-- before this migration runs): operational, never versioned or federated, so its
-- seq stays monotonic with no merge conflict. seq is NOT AUTO_INCREMENT — it is
-- drawn from the single-row bd_mutations_seq counter inside the mutation's own
-- transaction so concurrent allocators conflict and the surviving seqs are
-- gapless and commit-ordered (see main migration 0056 and issueops.nextMutationSeq).
-- Same __temp__ + conditional RENAME pattern for both tables: create only when
-- absent, never touch an existing table. See issueops.RecordMutationInTx for the writer.
DROP TABLE IF EXISTS __temp__bd_mutations_journal;
CREATE TABLE __temp__bd_mutations_journal (
    seq BIGINT NOT NULL PRIMARY KEY,
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

DROP TABLE IF EXISTS __temp__bd_mutations_seq;
CREATE TABLE __temp__bd_mutations_seq (
    id INT NOT NULL PRIMARY KEY,
    next_seq BIGINT NOT NULL
);
SET @seq_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bd_mutations_seq');
SET @seq_sql = IF(@seq_exists = 0, 'RENAME TABLE __temp__bd_mutations_seq TO bd_mutations_seq', 'DROP TABLE __temp__bd_mutations_seq');
PREPARE stmt FROM @seq_sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
-- Seed (idempotent), then raise to the journal high-water mark. See main
-- migration 0056 for why this is VALUES + GREATEST rather than INSERT ... SELECT.
INSERT IGNORE INTO bd_mutations_seq (id, next_seq) VALUES (0, 0);
UPDATE bd_mutations_seq
    SET next_seq = GREATEST(next_seq, COALESCE((SELECT MAX(seq) FROM bd_mutations_journal), 0))
    WHERE id = 0;
