-- +goose Up
-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE idempotency_keys;
-- +goose StatementEnd
