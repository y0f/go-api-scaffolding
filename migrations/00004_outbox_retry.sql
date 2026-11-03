-- +goose Up
-- +goose StatementBegin
-- A message the publisher rejects records the attempt and error and backs off
-- until next_attempt_at, so it never blocks the messages behind it. The
-- partial index keeps the retention purge's "published before" scan cheap.
ALTER TABLE outbox_messages
    ADD COLUMN attempts        INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN last_error      TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX outbox_published_at_idx ON outbox_messages (published_at) WHERE published_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX outbox_published_at_idx;
ALTER TABLE outbox_messages
    DROP COLUMN next_attempt_at,
    DROP COLUMN last_error,
    DROP COLUMN attempts;
-- +goose StatementEnd
