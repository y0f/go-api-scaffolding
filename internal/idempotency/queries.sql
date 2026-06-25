-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE key = $1 AND expires_at > now();

-- name: PutIdempotencyKey :execrows
INSERT INTO idempotency_keys (key, request_hash, response_status, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (key) DO NOTHING;

-- name: PurgeExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys
WHERE expires_at <= now();
