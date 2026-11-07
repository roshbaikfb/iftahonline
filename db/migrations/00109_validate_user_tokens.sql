-- Validates the user_tokens constraints 00108 added NOT VALID (C1): the
-- rows written before it are password and email links, which satisfy
-- them. VALIDATE takes a lock that lets reads and writes go on.

-- +goose Up
ALTER TABLE user_tokens VALIDATE CONSTRAINT user_tokens_purpose_check;
ALTER TABLE user_tokens VALIDATE CONSTRAINT user_tokens_support_ref;
ALTER TABLE user_tokens VALIDATE CONSTRAINT user_tokens_request_id_fkey;
ALTER TABLE user_tokens VALIDATE CONSTRAINT user_tokens_link_store_id_fkey;

-- +goose Down
-- Nothing to undo: a validated constraint stays as 00108 defined it.
SELECT 1;
