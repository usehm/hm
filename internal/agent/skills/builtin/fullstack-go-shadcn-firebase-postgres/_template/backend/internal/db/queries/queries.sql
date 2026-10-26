-- name: UpsertUser :one
INSERT INTO users (firebase_uid, email) VALUES ($1, $2)
ON CONFLICT (firebase_uid) DO UPDATE SET email = EXCLUDED.email
RETURNING id, firebase_uid, email, created_at;

-- name: ListItems :many
SELECT id, owner_id, title, done, created_at FROM items
WHERE owner_id = $1 ORDER BY created_at DESC;

-- name: CreateItem :one
INSERT INTO items (owner_id, title) VALUES ($1, $2)
RETURNING id, owner_id, title, done, created_at;

-- name: SetItemDone :one
UPDATE items SET done = $3 WHERE id = $1 AND owner_id = $2
RETURNING id, owner_id, title, done, created_at;

-- name: DeleteItem :execrows
DELETE FROM items WHERE id = $1 AND owner_id = $2;
