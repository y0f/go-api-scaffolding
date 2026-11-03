-- +goose NO TRANSACTION
-- Matches the list order (newest first, id as the tie-breaker), so a page is
-- read from the index instead of sorting the whole table. widgets already
-- holds data on an upgrade, so the replacement index is built CONCURRENTLY
-- next to the old one and swapped in by name; reads and writes keep flowing.
-- Each statement is idempotent, so a run that failed half-way (which can leave
-- an INVALID widgets_created_at_new_idx behind) can simply be re-run.

-- +goose Up
DROP INDEX CONCURRENTLY IF EXISTS widgets_created_at_new_idx;
CREATE INDEX CONCURRENTLY widgets_created_at_new_idx ON widgets (created_at DESC, id DESC);
DROP INDEX CONCURRENTLY IF EXISTS widgets_created_at_idx;
ALTER INDEX widgets_created_at_new_idx RENAME TO widgets_created_at_idx;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS widgets_created_at_new_idx;
CREATE INDEX CONCURRENTLY widgets_created_at_new_idx ON widgets (created_at DESC);
DROP INDEX CONCURRENTLY IF EXISTS widgets_created_at_idx;
ALTER INDEX widgets_created_at_new_idx RENAME TO widgets_created_at_idx;
