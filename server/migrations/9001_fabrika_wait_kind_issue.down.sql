-- Back to the 9000 list. NOT VALID so rows already written with kind = 'issue'
-- do not block the rollback; new 'issue' waits are rejected again.
ALTER TABLE stream_wait DROP CONSTRAINT IF EXISTS stream_wait_kind_check;
ALTER TABLE stream_wait ADD CONSTRAINT stream_wait_kind_check
    CHECK (kind IN ('ci', 'git-tag', 'http', 'process', 'time', 'gpu-window')) NOT VALID;
