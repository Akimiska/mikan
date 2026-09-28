#!/usr/bin/env sh
# Node e2e: builds mikan-node, starts real mihomo clients and runs the tests.
# KEEP=1 leaves the stack running for debugging.
set -eu
cd "$(dirname "$0")"
export MSYS_NO_PATHCONV=1

docker volume create mikan-gomod >/dev/null
docker volume create mikan-gocache >/dev/null
docker compose up -d driver
docker compose exec -T driver go run ./test/e2e/gen -out /work
docker compose up -d --build node target client-a client-b
sleep 3

status=0
docker compose exec -T driver go test -tags e2e -count=1 -v ./test/e2e/ || status=$?
if [ "$status" != 0 ]; then
  docker compose logs --tail 80 node
fi
if [ "${KEEP:-0}" != 1 ]; then
  docker compose down -v --remove-orphans
fi
exit "$status"
