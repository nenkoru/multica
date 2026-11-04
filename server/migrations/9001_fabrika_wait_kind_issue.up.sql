-- RUSL-2632: the contract validator accepts wait.kind = "issue" (validWaitKinds
-- in internal/streams/contract.go), but the CHECK from 9000 did not, so the
-- supervisor failed to record the wait on every tick. Keep both lists equal;
-- TestDBAcceptsEveryValidatorValue fails when they drift apart again.
ALTER TABLE stream_wait DROP CONSTRAINT IF EXISTS stream_wait_kind_check;
ALTER TABLE stream_wait ADD CONSTRAINT stream_wait_kind_check
    CHECK (kind IN ('ci', 'git-tag', 'http', 'process', 'time', 'gpu-window', 'issue'));
