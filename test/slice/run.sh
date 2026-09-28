#!/usr/bin/env sh
# Vertical slice: real image, bootstrap, panel + node, a mihomo client configured
# from the panel's own subscription. KEEP=1 leaves the stack running.
set -eu
cd "$(dirname "$0")"
export MSYS_NO_PATHCONV=1

docker volume create mikan-gomod >/dev/null
docker volume create mikan-gocache >/dev/null
docker compose down -v --remove-orphans >/dev/null 2>&1 || true
docker compose build node target

PW=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20)
echo "$PW" | docker compose run --rm -T panel admin bootstrap \
  --public-host node --port 2053 --admin-path slice-admin-path-0000 --sub-path slicesub0000 --username admin --password-stdin >/dev/null
docker compose up -d node panel target driver

status=0
docker compose exec -T -e SLICE_PW="$PW" driver go run ./test/slice/driver nodes || status=$?
if [ "$status" = 0 ]; then
  docker compose --profile node2 up -d node2
  docker compose exec -T -e SLICE_PW="$PW" driver go run ./test/slice/driver prepare || status=$?
fi
if [ "$status" = 0 ]; then
  docker compose --profile client up -d client
  sleep 3
  docker compose exec -T -e SLICE_PW="$PW" driver go run ./test/slice/driver verify || status=$?
fi
if [ "$status" != 0 ]; then
  docker compose --profile node2 logs --tail 60 panel node node2
  docker compose --profile client logs --tail 30 client
fi
if [ "${KEEP:-0}" != 1 ]; then
  docker compose --profile client --profile node2 down -v --remove-orphans
fi
exit "$status"
