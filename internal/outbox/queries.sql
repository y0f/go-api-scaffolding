-- name: EnqueueOutboxMessage :one
INSERT INTO outbox_messages (aggregate_id, event_type, payload)
VALUES ($1, $2, $3)
RETURNING *;

-- name: FetchUnpublishedOutbox :many
SELECT * FROM outbox_messages
WHERE published_at IS NULL
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxPublished :exec
UPDATE outbox_messages
SET published_at = now()
WHERE id = ANY(@ids::bigint[]);
