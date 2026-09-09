-- +goose Up
-- +goose StatementBegin
-- forge:begin example
CREATE TABLE widgets (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX widgets_created_at_idx ON widgets (created_at DESC);
-- forge:end example

-- forge:begin outbox
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
-- forge:end outbox

-- forge:begin idempotency
-- Idempotency keys let clients safely retry unsafe requests (POST) without
-- creating duplicates. The stored response is replayed for a repeated key.
CREATE TABLE idempotency_keys (
    key             TEXT        PRIMARY KEY,
    request_hash    TEXT        NOT NULL,
    response_status INT         NOT NULL,
    response_body   BYTEA       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);
-- forge:end idempotency
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- forge:begin idempotency
DROP TABLE idempotency_keys;
-- forge:end idempotency
-- forge:begin outbox
DROP TABLE outbox_messages;
-- forge:end outbox
-- forge:begin example
DROP TABLE widgets;
-- forge:end example
-- +goose StatementEnd
