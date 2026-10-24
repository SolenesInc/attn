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

case "${1:-}" in
start)
  if [[ -z "$(docker ps -q --filter "name=^${name}$")" ]]; then
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker run -d --name "$name" -p "127.0.0.1:${port}:8123" \
      -e CLICKHOUSE_USER=attn -e CLICKHOUSE_PASSWORD=attn \
      -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 "$image" >/dev/null
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
