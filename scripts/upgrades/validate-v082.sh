#!/usr/bin/env bash
# Disposable kjob only: synthetic data, loopback PostgreSQL, no production access.
# Run in pgvector/pgvector:pg17 with 4 CPU / 8 GiB, passing a server-only archive
# of e9c3e7fce5e4b1db463a438bdf051c630b23d0e1 as the first argument.
set -Eeuo pipefail
root=$PWD
baseline_archive=$(realpath "${1:?baseline server archive required}")
mkdir -p validation
export GOMAXPROCS=4
export GOFLAGS=-p=4
export PATH=/usr/local/go/bin:$PATH
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq > validation/setup.log 2>&1
apt-get install -y -qq --no-install-recommends curl ca-certificates gcc libc6-dev git python3 procps redis-server >> validation/setup.log 2>&1
curl -fsSL https://go.dev/dl/go1.26.9.linux-amd64.tar.gz -o /work/go.tar.gz
tar -C /usr/local -xzf /work/go.tar.gz
go version > validation/toolchain.txt
postgres --version >> validation/toolchain.txt
mkdir -p /work/pgdata /work/pgsocket /work/baseline
chown postgres:postgres /work/pgdata /work/pgsocket
runuser -u postgres -- initdb -D /work/pgdata --auth=trust >> validation/setup.log 2>&1
runuser -u postgres -- pg_ctl -D /work/pgdata -l /work/pgdata/server.log -o '-h 127.0.0.1 -k /work/pgsocket' start >> validation/setup.log 2>&1
trap 'runuser -u postgres -- pg_ctl -D /work/pgdata -m fast stop >/dev/null' EXIT
export PGHOST=127.0.0.1 PGUSER=postgres
redis-server --daemonize yes --bind 127.0.0.1 --port 6379 --save '' --appendonly no
export REDIS_TEST_URL=redis://127.0.0.1:6379/1
tar -C /work/baseline -xf "$baseline_archive"
createdb baseline
export DATABASE_URL='postgres://postgres@127.0.0.1:5432/baseline?sslmode=disable'
(cd /work/baseline/server && go run ./cmd/migrate up) > validation/baseline-migrate.log 2>&1
test "$(psql -d baseline -Atc 'SELECT count(*) FROM schema_migrations')" = 658
psql -v ON_ERROR_STOP=1 -d baseline <<'SQL' > validation/seed.log
INSERT INTO "user" (id, name, email) VALUES
 ('10000000-0000-4000-8000-000000000003', 'Synthetic upgrade user', 'upgrade@example.test');
INSERT INTO workspace (id, name, slug) VALUES
 ('10000000-0000-4000-8000-000000000000', 'Synthetic upgrade workspace', 'synthetic-upgrade');
INSERT INTO epic (id, workspace_id, title) VALUES
 ('10000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000000', 'Synthetic upgrade epic');
INSERT INTO sprint (workspace_id, name, start_date, end_date) VALUES
 ('10000000-0000-4000-8000-000000000000', 'Synthetic upgrade sprint', '2026-11-01', '2026-11-15');
INSERT INTO stream (workspace_id, slug, title, summary, active) VALUES
 ('10000000-0000-4000-8000-000000000000', 'synthetic-upgrade', 'Synthetic upgrade stream', 'Preserve this synthetic summary', false);
INSERT INTO issue (workspace_id, title, creator_type, creator_id, number, epic_id, sprint_id)
 SELECT '10000000-0000-4000-8000-000000000000', 'Synthetic linked issue', 'member',
 '10000000-0000-4000-8000-000000000003', 1, '10000000-0000-4000-8000-000000000001', id FROM sprint;
SQL
psql -d baseline -Atc "SELECT (SELECT count(*) FROM epic), (SELECT count(*) FROM sprint), (SELECT count(*) FROM stream), (SELECT count(*) FROM issue WHERE epic_id IS NOT NULL AND sprint_id IS NOT NULL), (SELECT summary FROM stream WHERE slug = 'synthetic-upgrade')" > validation/before.txt
pg_dump --format=custom -d baseline -f /work/baseline.dump
createdb restored
pg_restore --exit-on-error --no-owner -d restored /work/baseline.dump
export DATABASE_URL='postgres://postgres@127.0.0.1:5432/restored?sslmode=disable'
(cd server && go run ./cmd/migrate up) > validation/upgrade-migrate.log 2>&1
(cd server && go run ./cmd/migrate up) > validation/idempotent-migrate.log 2>&1
psql -d restored -Atc "SELECT (SELECT count(*) FROM epic), (SELECT count(*) FROM sprint), (SELECT count(*) FROM stream), (SELECT count(*) FROM issue WHERE epic_id IS NOT NULL AND sprint_id IS NOT NULL), (SELECT summary FROM stream WHERE slug = 'synthetic-upgrade')" > validation/after.txt
cmp validation/before.txt validation/after.txt
psql -d restored -Atc 'SELECT version FROM schema_migrations ORDER BY version' > validation/migration-names.txt
test "$(wc -l < validation/migration-names.txt)" = 678
for migration in server/migrations/*.up.sql; do
  basename "$migration" .up.sql
done | LC_ALL=C sort > validation/expected-migration-names.txt
LC_ALL=C sort validation/migration-names.txt > validation/applied-migration-names.txt
cmp validation/expected-migration-names.txt validation/applied-migration-names.txt
psql -d restored -Atc 'SELECT count(*) FROM pg_index WHERE NOT indisvalid OR NOT indisready' > validation/invalid-index-count.txt
test "$(cat validation/invalid-index-count.txt)" = 0
createdb tests
export DATABASE_URL='postgres://postgres@127.0.0.1:5432/tests?sslmode=disable'
(cd server && go run ./cmd/migrate up) > validation/fresh-migrate.log 2>&1
(cd server && go test -json -count=1 ./internal/migrations ./cmd/migrate ./cmd/server ./internal/handler ./internal/service ./internal/streams/... ./internal/projectaccess) > validation/db-tests.jsonl 2>&1
# This is a genuine isolated pod; fake Claude launch tests need the documented
# sandbox signal when its image runs as root. Production guards stay intact.
# Serialize packages and bound test concurrency for millisecond timing fixtures.
(cd server && IS_SANDBOX=1 go test -json -race -p=1 -parallel=2 -count=1 ./internal/daemon ./pkg/redact ./pkg/agent ./cmd/multica) > validation/race-tests.jsonl 2>&1
(cd server && go build ./cmd/server ./cmd/multica ./cmd/migrate) > validation/build.log 2>&1
python3 scripts/maintenance/test_operations.py > validation/maintenance.log 2>&1
printf 'baseline=658 restored=678 invalid_indexes=0 counters=unchanged\n' > validation/result.txt
