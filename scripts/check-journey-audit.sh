#!/usr/bin/env bash
set -euo pipefail
mkdir -p audit-artifacts/formatted
files=(server/internal/journeyaudit/*.go server/internal/middleware/journey_audit*.go server/internal/middleware/auth.go server/internal/events/bus.go server/internal/handler/{handler.go,issue.go,comment.go} server/internal/service/{issue.go,issue_wakeup_activity.go} server/cmd/server/activity_listeners.go server/internal/service/task.go server/internal/integrations/channel/engine/{session.go,journey_audit_db_test.go} server/internal/streams/{journey_audit.go,journey_audit_db_test.go,digest.go,multika.go,ask_buttons.go,http.go,supervisor.go} server/cmd/server/{router.go,router_streams.go})
gofmt -w "${files[@]}"
for p in "${files[@]}"; do cp --parents "$p" audit-artifacts/formatted; done
apt-get update -qq > audit-artifacts/apt.log 2>&1
apt-get install -y -qq postgresql > audit-artifacts/postgres-install.log 2>&1
pg_ctlcluster 15 main start
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE USER multica WITH PASSWORD 'synthetic-audit-only' SUPERUSER;" -c "CREATE DATABASE multica OWNER multica;" > audit-artifacts/postgres-setup.log
export DATABASE_URL='postgres://multica:synthetic-audit-only@localhost:5432/multica?sslmode=disable'
export GOMAXPROCS=4
cd server
go run ./cmd/migrate up > ../audit-artifacts/migrations.log 2>&1
go test -p 3 -count=1 -json ./internal/journeyaudit ./internal/middleware ./internal/streams ./internal/integrations/channel/engine ./internal/service ./internal/handler ./cmd/server -run 'Test(Auth|AppendUserMessage|BindMediaRefs|CredentialProvenance|AuditMetadataAndFlag|Journey|Multika|AskButtons|AskButton|Red|FabrikaMixedProjectGuard|Channel.*Provenance|.*DirectChat.*|.*Channel.*Persist.*)' > ../audit-artifacts/tests.jsonl 2>&1
go build -p 3 ./cmd/server ./cmd/multica > ../audit-artifacts/build.log 2>&1
echo AUDIT_GO_PASS
