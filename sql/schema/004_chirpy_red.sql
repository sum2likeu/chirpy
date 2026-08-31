-- +goose up
ALTER TABLE users
ADD is_chirpy_red BOOLEAN DEFAULT FALSE NOT NULL;

-- +goose down
ALTER TABLE users
DROP is_chirpy_red;