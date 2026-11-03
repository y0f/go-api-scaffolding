-- name: EnqueueOutboxMessage :exec
INSERT INTO outbox_messages (aggregate_id, event_type, payload)
VALUES ($1, $2, $3);

-- name: FetchUnpublishedOutbox :many
SELECT * FROM outbox_messages
WHERE published_at IS NULL AND next_attempt_at <= now()
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxPublished :exec
UPDATE outbox_messages
SET published_at = now()
WHERE id = ANY(@ids::bigint[]);

-- name: RecordOutboxFailure :one
-- Backs the message off exponentially (2s, 4s, 8s, ... capped at one
-- hour) so a message the publisher keeps rejecting is retried rarely instead
-- of on every poll.
UPDATE outbox_messages
SET attempts        = attempts + 1,
    last_error      = @last_error::text,
    next_attempt_at = now() + least(
        make_interval(secs => power(2, least(attempts + 1, 12))),
        interval '1 hour')
WHERE id = @id
RETURNING attempts;

-- name: DeletePublishedOutboxBefore :execrows
-- Deletes at most max_rows rows per call, so a large backlog is purged in
-- short transactions rather than one long one.
DELETE FROM outbox_messages
WHERE id IN (
    SELECT id FROM outbox_messages
    WHERE published_at IS NOT NULL AND published_at < @before::timestamptz
    LIMIT @max_rows
);
