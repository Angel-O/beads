-- Reverse migration 0056: drop the clone-local mutations journal tables.
-- Both are dolt_ignored, so this only touches the working set.
DROP TABLE IF EXISTS bd_mutations_journal;
DROP TABLE IF EXISTS bd_mutations_seq;
