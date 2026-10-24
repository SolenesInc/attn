# attn-ingest

`attn-ingest` (`cmd/attn-ingest`) is the public service that receives the
analytics an install shares and stores them in ClickHouse. An install
registers once, keeps the token it gets back, and sends its outbox as batches.
The envelope types it accepts are `analytics.Batch` and `analytics.Record`
(`internal/analytics`), the same ones the daemon writes.

## API

| Request | Reply |
|---|---|
| `POST /v1/installs` | `200 {install_id, token}` |
| `POST /v1/events`, `Authorization: Bearer <token>`, a gzip NDJSON batch | `200 {accepted, duplicates}` |
| `GET /v1/installs/me`, Bearer | `200 {install_id, created_at, events, streams, first_at, last_at}` |
| `DELETE /v1/installs/me`, Bearer | `204`: the install and all its events are gone |
| `GET /metrics` | Prometheus counters |
| `GET /healthz` | `200` while ClickHouse answers |

The token identifies an install, not a person. The service stores only its
SHA-256, and every `/v1/installs/me` request finds its install from the token,
so no request can name another install. After a delete the token no longer
works.

A batch body is gzip of newline-delimited JSON: the first line is
`{"build": {...}}`, the build that sent the batch, and every following line is
one record. `analytics.WriteBatch` writes it. Event names and streams the
service has never seen are accepted, so a newer build never loses data; the
shape and sizes are enforced, and a malformed line answers `400` naming the
line and the field.

A record is stored once per install, stream and seq. A retried batch answers
with its records counted as `duplicates` and adds no rows.

## Limits

Every refusal answers with the limit's name, its value and what this request
would make it, and `429` also says when to retry:

```json
{"limit": "events_per_install_per_minute", "value": 10000, "observed": 10200, "retry_after_s": 41}
```

| Limit | Value | Status | Receipt |
|---|---|---|---|
| `events_per_install_per_minute` | 10,000 in any 60 s | 429 | measured peak minute 882 |
| `events_per_install_per_day` | 1,000,000 in any 24 h | 429 | measured peak day 87,414 |
| `installs_per_ip_per_hour` | 20 in any hour | 429 | guessed; remeasure from real registrations |
| `records_per_batch` | 10,000 | 413 | the minute limit; a larger batch could never be accepted |
| `request_body_gzip_bytes` | 4 MiB | 413 | a redacted peak day is 613 KB gzipped |
| `request_body_bytes` | 64 MiB | 413 | a redacted peak day is 10.9 MB uncompressed |
| `record_line_bytes` | 64 KiB | 413 | 4× the 16 KB vent text limit |

Records count against the per-install limits whether or not they turn out to
be duplicates; a refused batch counts nothing. The counts live in memory and
reset when the service restarts. Read and write timeouts are 2 minutes: 4 MiB
at 1 Mbit/s takes 32 s.

Every stored row keeps a keyed hash of the address that sent it
(HMAC-SHA256 with `ATTN_INGEST_IP_SECRET`), never the address.

## Quarantine

Insert an install id or an address hash into the `quarantine` table and that
install or address is refused with `403` and the reason, on registration and
on every batch:

```sql
INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('i-…', '', 'flooded garbage names', now64());
DELETE FROM events WHERE install_id IN (SELECT install_id FROM quarantine WHERE install_id != '');
```

## Configuration

| Variable | Meaning |
|---|---|
| `ATTN_INGEST_CLICKHOUSE_URL` | Required. ClickHouse's HTTP interface with credentials and the database, such as `http://user:password@clickhouse:8123/?database=attn`. The service creates its tables there on start. |
| `ATTN_INGEST_IP_SECRET` | Required, at least 32 bytes. Rotating it stops new rows' address hashes from matching older ones. |
| `ATTN_INGEST_LISTEN` | Listen address, `:8080` by default. |
| `ATTN_INGEST_CLIENT_IP_HEADER` | The header that carries the client address behind a proxy, such as `CF-Connecting-IP`. Unset, the connection's address is used. Set it only where the proxy is the only way in. |

`/metrics` and `/healthz` are served on the same address as the API; expose
only `/v1/` publicly.

## Running locally

```bash
make ingest-clickhouse            # starts ClickHouse 25.8 in Docker and prints its URL
ATTN_INGEST_CLICKHOUSE_URL='http://attn:attn@127.0.0.1:18123/?database=default' \
ATTN_INGEST_IP_SECRET=0123456789abcdef0123456789abcdef \
ATTN_INGEST_LISTEN=127.0.0.1:8080 go run ./cmd/attn-ingest
make ingest-clickhouse-stop
```

## Testing

The stack tests in `cmd/attn-ingest` run the built service against a real
ClickHouse, each in its own database, through `testworld.NewClickHouse` and
`testworld.StartIngest`. They need `ATTN_TEST_CLICKHOUSE_URL`:

```bash
ATTN_TEST_CLICKHOUSE_URL=$(make -s ingest-clickhouse) go test ./cmd/attn-ingest
```

Without it they skip locally and fail under CI, where the Daemon job starts
the same container before the Go suite.

## ClickHouse memory

Measured on 2026-10-24 with ClickHouse 25.8.33.6 in Docker under a 1 GiB
limit, loading 87,414 redacted rows built from the busiest day of a copy of a
production database (2026-09-27, 87,413 facts), then running per-hour and
per-subject aggregates:

| | Resident | cgroup peak |
|---|---|---|
| Empty | 420 MiB | 467 MiB |
| Peak day loaded | 466 MiB | 597 MiB |
| After queries | 505 MiB | 658 MiB |

The day takes 697 KiB on disk (10.2 MiB uncompressed). Most of the memory is
ClickHouse's own baseline, not the data.
