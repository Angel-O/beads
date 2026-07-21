-- Create the durable events journal table (bd-events-journal).
--
-- bd_events_journal is an append-only, seq-ordered record of every committed
-- issue mutation, written in the SAME transaction as the mutation itself at the
-- shared issueops seam (see issueops.RecordEventInTx). External tooling reads
-- it through `bd events tail --since <seq>` / `bd events export` to replay
-- the exact mutation history of the workspace.
--
-- The table is clone-local (registered dolt_ignored, seeded by MigrateUp's
-- doltIgnorePatterns before this migration runs): it is operational state, never
-- versioned or federated, so its seq stays monotonic without ever producing a
-- merge conflict.
--
-- seq is NOT AUTO_INCREMENT. AUTO_INCREMENT assigns at INSERT, not at commit, so
-- under concurrent transactions (the shared SQL server) a lower seq can commit
-- AFTER a higher seq becomes visible, and a consumer tailing WHERE seq > cursor
-- permanently skips it. Instead seq is drawn from the single-row counter table
-- bd_events_seq inside the mutation's own transaction (issueops.nextEventSeq):
-- the shared counter row makes concurrent allocators conflict, so exactly one
-- commit order survives and the surviving seqs are gapless and commit-ordered
-- (a rolled-back allocator burns no seq). The counter table is dolt_ignored too,
-- so it shares the journal's working-set locality.
--
-- Fresh clones never run this migration (the schema_migrations cursor arrives
-- at-latest); they materialize both tables via ignored migration 0014. The
-- __temp__ + conditional RENAME dance keeps each CREATE idempotent so a re-run,
-- or a run against a workspace that already has the tables, is a no-op.
DROP TABLE IF EXISTS __temp__bd_events_journal;
CREATE TABLE __temp__bd_events_journal (
    seq BIGINT NOT NULL PRIMARY KEY,
    ts DATETIME NOT NULL,
    op VARCHAR(32) NOT NULL,
    issue_id VARCHAR(255) NOT NULL,
    issue_json LONGTEXT,
    dep_json TEXT,
    INDEX idx_bd_events_journal_issue (issue_id)
);
SET @exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bd_events_journal');
SET @sql = IF(@exists = 0, 'RENAME TABLE __temp__bd_events_journal TO bd_events_journal', 'DROP TABLE __temp__bd_events_journal');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

DROP TABLE IF EXISTS __temp__bd_events_seq;
CREATE TABLE __temp__bd_events_seq (
    id INT NOT NULL PRIMARY KEY,
    next_seq BIGINT NOT NULL
);
SET @seq_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bd_events_seq');
SET @seq_sql = IF(@seq_exists = 0, 'RENAME TABLE __temp__bd_events_seq TO bd_events_seq', 'DROP TABLE __temp__bd_events_seq');
PREPARE stmt FROM @seq_sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
-- Seed the counter row (idempotent), then raise it to the journal's current
-- high-water mark so seq never resets or collides (0 on a fresh table). Done as
-- VALUES + a GREATEST update rather than INSERT ... SELECT MAX(): in Dolt a
-- SELECT that mixes a literal with an aggregate over an EMPTY table yields zero
-- rows, so an INSERT ... SELECT would seed nothing on a fresh workspace.
INSERT IGNORE INTO bd_events_seq (id, next_seq) VALUES (0, 0);
UPDATE bd_events_seq
    SET next_seq = GREATEST(next_seq, COALESCE((SELECT MAX(seq) FROM bd_events_journal), 0))
    WHERE id = 0;
