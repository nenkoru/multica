set -u
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq curl ca-certificates gnupg lsb-release >/dev/null 2>&1
install -d /usr/share/postgresql-common/pgdg
curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc || { echo KEY FAIL; exit 89; }
echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq postgresql-17 postgresql-17-pgvector >/dev/null 2>&1 || { echo "APT FAIL"; exit 90; }
PGV=17
pg_ctlcluster $PGV main start || exit 91
sleep 3
su postgres -c "psql -qc \"ALTER USER postgres PASSWORD 'pw'\"" 
su postgres -c "createdb multica"
export DATABASE_URL='postgres://postgres:pw@localhost:5432/multica?sslmode=disable'
export DATABASE_URL='postgres://postgres:pw@localhost:5432/multica?sslmode=disable'
(cd server && go run ./cmd/migrate up >/dev/null 2>&1)
echo "=== merged tests"
(cd server && go test -p 2 ./... 2>&1 | grep -E '^(--- FAIL|FAIL|panic)' | sort | uniq > /tmp/merged.txt; cat /tmp/merged.txt)
echo "=== baseline v0.7.2 (same packages)"
su postgres -c "createdb base"
export DATABASE_URL='postgres://postgres:pw@localhost:5432/base?sslmode=disable'
(cd .base/server && go run ./cmd/migrate up >/dev/null 2>&1)
PK=$(grep '^FAIL\s' /tmp/merged.txt | awk '{print $2}' | sed 's#github.com/multica-ai/multica/server#.#' | tr '\n' ' ')
echo "pkgs: $PK"
(cd .base/server && go test -p 2 $PK 2>&1 | grep -E '^(--- FAIL|FAIL|panic)' | sort | uniq)
echo DONE
