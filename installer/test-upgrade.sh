#!/bin/sh
# Fault-injection tests of the real host updater. Run ONLY inside a disposable Linux
# container; Docker is replaced by a recording stand-in, no real daemon is contacted.
set -eu
[ "${MIKAN_INSTALLER_TEST:-}" = 1 ] || { echo 'Set MIKAN_INSTALLER_TEST=1 inside a disposable container' >&2; exit 1; }
[ ! -e /opt/mikan ] || { echo '/opt/mikan already exists; refusing to touch it' >&2; exit 1; }
bin=$(realpath "${1:-target/debug/mikan}")
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" /opt/mikan' EXIT INT TERM
mkdir -p "$tmp/bin"
export TEST_STATE="$tmp"
export PATH="$tmp/bin:$PATH"
cat >"$tmp/bin/docker" <<'DOCKER'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$TEST_STATE/trace"
if [ "$1" = pull ]; then exit 0; fi
if [ "$1" = run ]; then echo 0.5.0.1; exit 0; fi
[ "$1" = compose ] || exit 1
shift 3
new() { grep -q "^MIKAN_IMAGE=new-image" /opt/mikan/.env; }
case "$*" in
  'exec -T panel mikan admin backup /data/panel/backup.db')
    cp /opt/mikan/data/panel/mikan.db /opt/mikan/data/panel/backup.db ;;
  'run --rm --no-deps -T panel database backup /data/panel/backup.dump')
    cp "$TEST_STATE/pg" /opt/mikan/data/panel/backup.dump ;;
  'run --rm --no-deps -T panel database migrate')
    [ -f "$TEST_STATE/pg" ] || echo 'imported users, payment42' >"$TEST_STATE/pg"
    echo '{"committed":true}' >/opt/mikan/data/panel/postgres-migration.json
    [ ! -f "$TEST_STATE/migrate-fail" ] || { echo 'lost response after commit' >&2; exit 1; }
    echo "PostgreSQL schema version: $(cat "$TEST_STATE/schema" 2>/dev/null || echo "1 -> 1")" ;;
  'up -d --wait --wait-timeout 120 postgres')
    [ ! -f "$TEST_STATE/pg-fail" ] || { echo 'database startup failed' >&2; exit 1; }
    if [ -f "$TEST_STATE/pg-fail-new" ] && new; then echo "database startup failed" >&2; exit 1; fi ;;
  'up -d')
    [ ! -f "$TEST_STATE/start-fail" ] || { echo 'panel startup failed' >&2; exit 1; }
    if [ -f "$TEST_STATE/start-fail-new" ] && new; then echo "new panel startup failed" >&2; exit 1; fi
    touch "$TEST_STATE/healthy" ;;
  'exec -T panel mikan health')
    [ -f "$TEST_STATE/healthy" ] ;;
  'stop panel') rm -f "$TEST_STATE/healthy" ;;
  *) exit 0 ;;
esac
DOCKER
chmod +x "$tmp/bin/docker"

fixture() {
  rm -rf /opt/mikan
  mkdir -p /opt/mikan/data/panel /opt/mikan/data/node
  printf 'MIKAN_IMAGE=old-image\nMIKAN_VERSION=0.4.4\nPANEL_PORT=21355\n' >/opt/mikan/.env
  printf 'legacy compose\n' >/opt/mikan/compose.yaml
  printf 'original users, payment42\n' >/opt/mikan/data/panel/mikan.db
  printf 'last WAL transaction\n' >/opt/mikan/data/panel/mikan.db-wal
  rm -f "$tmp/pg" "$tmp/trace" "$tmp/healthy" "$tmp/start-fail" "$tmp/pg-fail" "$tmp/migrate-fail" \
    "$tmp/schema" "$tmp/pg-fail-new" "$tmp/start-fail-new"
}

# A server already on PostgreSQL (0.5.0.0 and later).
pg_fixture() {
  fixture
  rm -f /opt/mikan/data/panel/mikan.db /opt/mikan/data/panel/mikan.db-wal
  printf 'MIKAN_IMAGE=old-image\nMIKAN_VERSION=0.5.0.0\nPANEL_PORT=21355\nMIKAN_POSTGRES_PASSWORD=livepassword\nMIKAN_DATABASE_URL=postgresql://mikan:livepassword@localhost/mikan?host=/run/postgresql\n' >/opt/mikan/.env
  echo 'live users, payment77' >"$tmp/pg"
}

fixture
touch "$tmp/pg-fail"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'startup fault must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=old-image' /opt/mikan/.env
grep -q 'legacy compose' /opt/mikan/compose.yaml
grep -q 'original users' /opt/mikan/data/panel/mikan.db
[ ! -e /opt/mikan/data/panel/postgres-migration.json ]
grep -q 'stop postgres' "$tmp/trace"
# The database volume may already be initialized with this password: it must survive.
password=$(grep '^MIKAN_POSTGRES_PASSWORD=' /opt/mikan/.env)
[ -n "$password" ]
if grep -q 'MIKAN_DATABASE_URL=' /opt/mikan/.env; then echo 'unexpected: MIKAN_DATABASE_URL=' >&2; exit 1; fi
rm "$tmp/pg-fail"
"$bin" update new-image >"$tmp/output" 2>&1
grep -qx "$password" /opt/mikan/.env
grep -q "MIKAN_DATABASE_URL=postgresql://mikan:${password#MIKAN_POSTGRES_PASSWORD=}@" /opt/mikan/.env
[ -e "$tmp/healthy" ]

# PostgreSQL to PostgreSQL: a database that cannot start brings the previous version back.
pg_fixture
touch "$tmp/pg-fail-new"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'database fault must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=old-image' /opt/mikan/.env
grep -q 'MIKAN_POSTGRES_PASSWORD=livepassword' /opt/mikan/.env
[ -e "$tmp/healthy" ]
if grep -q 'stop postgres' "$tmp/trace"; then echo 'unexpected: stop postgres' >&2; exit 1; fi

# An unchanged schema lets a release that does not start go back to the previous image.
pg_fixture
touch "$tmp/start-fail-new"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'startup fault must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=old-image' /opt/mikan/.env
grep -q 'MIKAN_DATABASE_URL=' /opt/mikan/.env
[ -e "$tmp/healthy" ]
grep -q 'runs again' "$tmp/output"

# A schema the new release moved forward stays: the previous binary would refuse it.
pg_fixture
touch "$tmp/start-fail-new"
echo '1 -> 2' >"$tmp/schema"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'startup fault must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=new-image' /opt/mikan/.env
[ ! -e "$tmp/healthy" ]
grep -q 'No automatic database rollback is safe' "$tmp/output"

fixture
touch "$tmp/migrate-fail"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'lost migration response must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=new-image' /opt/mikan/.env
grep -q 'MIKAN_DATABASE_URL=' /opt/mikan/.env
[ ! -e "$tmp/healthy" ]
echo 'new post-import payment43' >>"$tmp/pg"
rm "$tmp/migrate-fail"
"$bin" update new-image >"$tmp/output" 2>&1
grep -q 'payment43' "$tmp/pg"
grep -q 'original users' /opt/mikan/data/panel/mikan.db
[ -e "$tmp/healthy" ]

fixture
touch "$tmp/start-fail"
if "$bin" update new-image >"$tmp/output" 2>&1; then echo 'panel startup fault must fail' >&2; exit 1; fi
grep -q 'MIKAN_IMAGE=new-image' /opt/mikan/.env
grep -q 'imported users' "$tmp/pg"
[ ! -e "$tmp/healthy" ]
grep -q 'No automatic database rollback is safe' "$tmp/output"
stop=$(grep -n 'stop panel' "$tmp/trace" | head -1 | cut -d: -f1)
migrate=$(grep -n 'panel database migrate' "$tmp/trace" | head -1 | cut -d: -f1)
[ "$stop" -lt "$migrate" ]
echo 'host updater: provisioning rollback, kept database password, PostgreSQL rollback on an unchanged schema, uncertain commit retry, preserved post-import writes, startup failure and writer-stop order passed'
