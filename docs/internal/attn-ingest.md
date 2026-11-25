# attn-ingest

`cmd/attn-ingest` (`internal/ingest/`) receives analytics batches and writes
them to ClickHouse. Read `internal/ingest/` for routes, ClickHouse views,
env vars and limit constants (receipts sit on the constants in `server.go` and
`batch.go`).

## Running locally

```sh
make ingest-clickhouse            # starts ClickHouse 25.8 in Docker and prints its URL
ATTN_INGEST_CLICKHOUSE_URL='http://attn:attn@127.0.0.1:18123/?database=default' \
ATTN_INGEST_IP_SECRET=0123456789abcdef0123456789abcdef \
ATTN_INGEST_LISTEN=127.0.0.1:8080 go run ./cmd/attn-ingest
make ingest-clickhouse-stop
```

Only the production instance uploads to `https://attn-telemetry.caketroll.cloud`
by default; set `ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:8080` on a named
instance to test uploads. Expose only `/v1/` publicly (`/metrics` and `/healthz`
share the listen address).

## Testing and the fake ClickHouse

Stack tests (`cmd/attn-ingest`) run against `internal/fakeclickhouse` by default
and against ClickHouse 25.8 in CI (`ATTN_TEST_CLICKHOUSE_URL`). A landing runs
against the fake only—whenever you change SQL that `attn-ingest` sends, run
against real ClickHouse before landing (pass `-count=1` so Go's cache does not
hide server behavior):

```sh
ATTN_TEST_CLICKHOUSE_URL="$(make ingest-clickhouse)" go test -count=1 ./cmd/attn-ingest
```

- When changing an analytics record's fields, update its view in
  `internal/ingest/views.go` in the same commit. The service drops and
  recreates views (`SQL SECURITY DEFINER`) on every start, so its ClickHouse
  user needs `CREATE VIEW` and `DROP VIEW`.
- When `attn-ingest` needs new SQL, teach `internal/fakeclickhouse` in the same
  change, matching ClickHouse 25.8's exact answer and edge cases. The shared
  stack tests are the fake's only test.

## Operations and receipts

- **Image:** `.github/workflows/ingest-image.yml` publishes
  `ghcr.io/solenesinc/attn-ingest:sha-<short>` on `main` pushes touching the
  service.
- **Quarantine an install or IP hash** (returns `403` on registration and
  batches):

  ```sql
  INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('i-…', '', 'reason', now64());
  DELETE FROM events WHERE install_id IN (SELECT install_id FROM quarantine WHERE install_id != '');
  ```

- **ClickHouse memory receipt** (2026-10-24, ClickHouse 25.8.33.6, 1 GiB limit,
  87,414 redacted peak-day rows = 697 KiB on disk): 420 MiB RSS empty, 466 MiB
  loaded, 505 MiB after queries (658 MiB cgroup peak).
