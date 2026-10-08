#!/usr/bin/env bash
# Disposable PostgreSQL inside a kjob container; never point at production.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq postgresql postgresql-contrib > /work/planning-postgres-install.log
pg_bindir=$(pg_config --bindir)
install -d -o postgres -g postgres /work/planning-pg
runuser -u postgres -- "$pg_bindir/initdb" -D /work/planning-pg -A trust > /work/planning-pg-init.log
runuser -u postgres -- "$pg_bindir/pg_ctl" -D /work/planning-pg -l /work/planning-pg.log -o '-h 127.0.0.1 -p 5432' start
"$pg_bindir/createdb" -h 127.0.0.1 -U postgres multica_planning
export DATABASE_URL='postgres://postgres@127.0.0.1:5432/multica_planning?sslmode=disable'
export GOMAXPROCS=2
export GOCACHE=/work/planning-go-cache
cd server
gofmt -w internal/handler/workspace.go internal/handler/workspace_test.go internal/handler/workspace_delete_manifest_test.go internal/handler/search_candidate_parity_test.go cmd/multica/cmd_planning*.go cmd/multica/cmd_issue.go cmd/multica/main.go cmd/migrate/migrate_planning_test.go cmd/migrate/main.go internal/handler/{epic,sprint,planning_test,issue,issue_table_query,issue_table_rows,handler,config,handler_test}.go internal/service/{planning,issue,task}.go
go build -o bin/multica ./cmd/multica
go build -o bin/server ./cmd/server
go build -o bin/migrate ./cmd/migrate
./bin/migrate up > /work/planning-migrations.log
go test -p 2 ./internal/handler ./cmd/multica ./cmd/migrate -run 'Test(Epic|Sprint|CreateEpic|UpdateEpic|GetEpic|ListEpics|CreateSprint|UpdateSprint|GetSprint|ListSprints|Planning|IssueToMap)' -count=1 -v > /work/planning-tests.log
cat /work/planning-tests.log

cd ..
export GOFLAGS='-p=2'
useradd -m -d /work/planning-test-home planningtests
chown -R planningtests:planningtests /work
runuser -u planningtests -- env GOCACHE=/work/planning-go-cache GOMODCACHE=/go/pkg/mod GOFLAGS=-p=2 GOMAXPROCS=2 DATABASE_URL="$DATABASE_URL" bash scripts/test-go.sh --race > /work/planning-full-go-tests.log 2>&1
