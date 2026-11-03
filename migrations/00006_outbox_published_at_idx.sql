-- +goose NO TRANSACTION
-- +goose Up
-- The partial index keeps the retention purge's "published before" scan cheap.
-- It is built concurrently so outbox inserts, and with them every write that
-- enqueues an event, keep running on a table that already holds rows. A
-- concurrent build that fails leaves an invalid index behind, so it is dropped
-- before the retry builds it again.
DROP INDEX CONCURRENTLY IF EXISTS outbox_published_at_idx;
CREATE INDEX CONCURRENTLY outbox_published_at_idx ON outbox_messages (published_at) WHERE published_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS outbox_published_at_idx;
