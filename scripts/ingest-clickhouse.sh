#!/usr/bin/env bash
set -euo pipefail

# The ClickHouse that attn-ingest tests and local runs use. Prints the HTTP URL
# for ATTN_TEST_CLICKHOUSE_URL. Startup took 1.9s on an M-series Mac
# (2026-10-24); 60s is the tripwire.

image=clickhouse/clickhouse-server:25.8.33.6
name=attn-ingest-clickhouse
port="${ATTN_INGEST_CLICKHOUSE_PORT:-18123}"
url="http://attn:attn@127.0.0.1:${port}/"
ready_seconds=60
docker_unreachable=3

case "${1:-}" in
start)
  if ! command -v docker >/dev/null; then
    echo "ClickHouse needs Docker, and there is no docker command: install OrbStack or Docker" >&2
    exit "$docker_unreachable"
  fi
  if ! docker info >/dev/null 2>&1; then
    echo "ClickHouse needs Docker, and no Docker daemon answers: start OrbStack or Docker" >&2
    exit "$docker_unreachable"
  fi
  existing="$(docker inspect --format '{{.Id}} {{.Config.Image}}' "$name" 2>/dev/null || true)"
  read -r existing_id existing_image <<<"$existing"
  if [[ -n "$existing_id" && "$existing_image" != "$image" ]]; then
    docker rm -f "$existing_id" >/dev/null 2>&1 || true
  fi
  if ! docker start "$name" >/dev/null 2>&1; then
    if ! run_output="$(docker run -d --name "$name" -p "127.0.0.1:${port}:8123" \
      -e CLICKHOUSE_USER=attn -e CLICKHOUSE_PASSWORD=attn \
      -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 "$image" 2>&1)"; then
      [[ "$run_output" == *Conflict* ]] || { echo "$run_output" >&2; exit 1; }
    fi
  fi
  deadline=$((SECONDS + ready_seconds))
  until curl -sf "$url" --data-binary 'SELECT 1' >/dev/null; do
    if ((SECONDS >= deadline)); then
      echo "ClickHouse in container $name did not answer on $url within ${ready_seconds}s" >&2
      docker logs --tail 50 "$name" >&2
      exit 1
    fi
    sleep 0.2
  done
  echo "$url"
  ;;
stop)
  docker rm -f "$name" >/dev/null
  ;;
*)
  echo "usage: $0 start|stop" >&2
  exit 2
  ;;
esac
