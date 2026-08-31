-- name: CreateRefreshToken :exec
INSERT INTO tokens (token, created_at, updated_at, user_id, expires_at)
VALUES (
    $1,
    NOW(),
    NOW(),
    $2,
    $3
);