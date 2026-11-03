-- +goose Up
-- +goose StatementBegin
-- A message the publisher rejects records the attempt and error and backs off
-- until next_attempt_at, so it never blocks the messages behind it. The
-- defaults are non-volatile, so adding the columns does not rewrite the table.
ALTER TABLE outbox_messages
    ADD COLUMN attempts        INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN last_error      TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbox_messages
    DROP COLUMN next_attempt_at,
    DROP COLUMN last_error,
    DROP COLUMN attempts;
-- +goose StatementEnd
