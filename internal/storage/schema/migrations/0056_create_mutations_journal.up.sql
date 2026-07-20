-- Create the durable mutations journal table (bd-mutations-journal).
--
-- bd_mutations_journal is an append-only, seq-ordered record of every committed
-- issue mutation, written in the SAME transaction as the mutation itself at the
-- shared issueops seam (see issueops.RecordMutationInTx). External tooling reads
-- it through `bd mutations tail --since <seq>` / `bd mutations export` to replay
-- the exact mutation history of the workspace.
--
-- The table is clone-local (registered dolt_ignored, seeded by MigrateUp's
-- doltIgnorePatterns before this migration runs): it is operational state, never
-- versioned or federated, so its AUTO_INCREMENT seq stays monotonic and
-- collision-free without ever producing a merge conflict. That is exactly why
-- the federation-conflict reasoning behind migration 0037 (which dropped
-- AUTO_INCREMENT from the *versioned* tables) does not apply here.
--
-- Fresh clones never run this migration (the schema_migrations cursor arrives
-- at-latest); they materialize the table via ignored migration 0014. The
-- __temp__ + conditional RENAME dance keeps the CREATE idempotent so a re-run,
-- or a run against a workspace that already has the table, is a no-op.
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
