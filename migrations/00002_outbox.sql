-- +goose Up
-- +goose StatementBegin
-- Transactional outbox: events are written in the same transaction as the
-- state change that produced them, then relayed by a separate poller. The
-- partial index keeps the poller's "unpublished" scan cheap as the table grows.
CREATE TABLE outbox_messages (
    id           BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_id UUID        NOT NULL,
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX outbox_unpublished_idx ON outbox_messages (id) WHERE published_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outbox_messages;
-- +goose StatementEnd
