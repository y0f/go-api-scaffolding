-- name: CreateWidget :one
INSERT INTO widgets (name, description, status)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWidget :one
SELECT * FROM widgets
WHERE id = $1;

-- name: ListWidgets :many
SELECT * FROM widgets
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountWidgets :one
SELECT count(*) FROM widgets;

-- name: UpdateWidget :one
UPDATE widgets
SET name        = $2,
    description = $3,
    status      = $4,
    updated_at  = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWidget :execrows
DELETE FROM widgets
WHERE id = $1;
