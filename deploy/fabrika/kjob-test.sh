#!/usr/bin/env bash
# Fabrika: backend checks in one pod (kjob), same Postgres as upstream CI.
#   kjob run --cpu 6 --mem 8Gi --image pgvector/pgvector:pg17 --timeout 90m -- bash deploy/fabrika/kjob-test.sh
# Extra `go test` packages can be passed as arguments.
set -euo pipefail

export POSTGRES_DB=multica POSTGRES_USER=multica POSTGRES_PASSWORD=multica
export DATABASE_URL="postgres://multica:multica@localhost:5432/multica?sslmode=disable"
export GOFLAGS=-count=1 GOTOOLCHAIN=auto

apt-get update -qq >/dev/null && apt-get install -y -qq curl ca-certificates git gcc >/dev/null
docker-entrypoint.sh postgres >/tmp/pg.log 2>&1 &
curl -fsSL https://go.dev/dl/go1.26.9.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
for _ in $(seq 60); do pg_isready -q -h localhost -U multica -d multica && break; sleep 2; done
pg_isready -h localhost -U multica -d multica

cd server
if [ "${FABRIKA_FIX:-}" = 1 ]; then
  # Dev mode: regenerate sqlc and gofmt in place; fetch the tree with --artifact.
  echo "== sqlc + gofmt -w"; go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate -f sqlc.streams.yaml && gofmt -w internal/streams cmd/server
fi
echo "== gofmt"; test -z "$(gofmt -l internal/streams cmd/server)" || { gofmt -l internal/streams cmd/server; exit 1; }
echo "== migrate up"; go run ./cmd/migrate up 2>&1 | tail -3
echo "== migrate down/up 9000"; psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f migrations/9000_fabrika_streams.down.sql && psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f migrations/9000_fabrika_streams.up.sql && echo ok
echo "== vet"; go vet ./internal/streams/... ./cmd/server/
echo "== build"; go build -o /tmp/server ./cmd/server && echo ok
echo "== test"; set +e; go test ./internal/streams/... ./internal/migrations/... "$@" > /tmp/test.log 2>&1; rc=$?; tail -15 /tmp/test.log; echo "== failures"; grep -n "_test.go:[0-9]*:" /tmp/test.log | head -40; exit $rc
