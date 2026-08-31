-- name: RevokeToken :exec
UPDATE tokens
SET 
updated_at = NOW(),
revoked_at = NOW()
    
WHERE token = $1;