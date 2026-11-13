# attn-ingest

`attn-ingest` (`cmd/attn-ingest`) is the public service that receives the
analytics an install shares and stores them in ClickHouse. An install
registers once, keeps the token it gets back, and sends its outbox as batches.
A batch is an `analytics.Batch` of `analytics.Record`s (`internal/analytics`).
The records are the ones the daemon writes to its outbox, and
[Telemetry](../guide/reference/telemetry.md) lists what they hold.

## API

| Request | Reply |
|---|---|
| `POST /v1/installs` | `200 {install_id, token}` |
| `POST /v1/events`, `Authorization: Bearer <token>`, a gzip NDJSON batch | `200 {accepted, duplicates}` |
| `DELETE /v1/installs/me`, Bearer | `204`: the install and all its events are gone |
| `GET /metrics` | Prometheus counters |
| `GET /healthz` | `200` while ClickHouse answers |

`attn_ingest_batch_bytes_accepted_total` counts the gzip bytes of every batch
the service answers `200`, duplicates included. The deployment has an alert on
this volume.

The token identifies an install, not a person. The service stores only the
token's SHA-256. On every request that carries a token, the service finds the
install from the token, so no request can name another install. After a delete,
the token no longer works.

A batch body is gzip of newline-delimited JSON. The first line is
`{"build": {...}}`, the build that recorded every record in the batch. Every
following line is one record. `analytics.WriteBatch` writes the body. The service accepts event names
and streams it has never seen, so a newer build never loses data. The service
enforces the shape and sizes, and a malformed line gets a `400` that names the
line and the field.

A record is stored once per install, stream, and seq. The service answers a
retried batch with its records counted as `duplicates` and adds no rows.

## Views

`events` keeps each record's fields as JSON in `props`. On start, the service
also creates one view per kind of record, with a typed column for each field
that [Telemetry](../guide/reference/telemetry.md) lists for the record, under
the field's own name. A nested field takes its last name (`session.harness` is
`harness`), except `tokens`, whose fields are `tokens_input`, `tokens_output`,
`tokens_cache_read` and `tokens_cache_write`. Query the views. Read `events`
for a record no view covers, such as bus facts, and for the fields no view
carries, such as the daemon's own memory, goroutines and database size in a
`process` sample.

Every view has `install_id`, `at` and the build's columns: `build_version`,
`build_commit`, `build_channel`, `build_time`, `build_fingerprint`,
`build_protocol`, `build_os`, `build_arch`, `build_dirty`, `app_version` and
`app_channel`. Every view except `app_actions` has `outpost`, the outpost's hash
for a record made on an outpost, and empty on the home.

A column is `NULL` only where a missing field means "unknown": `usd`, the
`tokens_*` columns, `user_input_ms`, `wait_ms`, `footprint_bytes`, the three
`split` columns of `process_samples`, and `daemon_uptime_s`. Any other missing
field reads as an empty string, `0` or `false`, so a session with no `model`
yet has `model = ''`.

| View | One row per | Columns besides `install_id`, `at`, `outpost` and the build columns |
|---|---|---|
| `runs` | agent run | `session`, `started_by`, `duration_ms`, `ended`, `opened_turn`, `quiet_stop`, `turn_requested`, `turn_after_stop`, `user_input_ms`, `approvals`, `usage`, `tokens_input`, `tokens_output`, `tokens_cache_read`, `tokens_cache_write`, `usd`, `harness`, `model`, `model_requested`, `launch` |
| `quiet_stops` | quiet stop verdict, with its run's fields | `session`, `verdict`, `failure`, `turn_after_stop`, `turn_requested`, `started_by`, `harness`, `model`, `launch` |
| `cli_commands` | `attn` command since 2026-11-02, when commands began to be named by their typed path | `session`, `command` (hooks start with `_`), `caller`, `harness`, `ok`, `exit_code`, `error_kind`, `refusal` |
| `process_samples` | process role in a `process` sample | `daemon_run`, `daemon_uptime_s` (`NULL` without the run's `daemon_start`), `role`, `shell`, `count`, `resident_bytes`, `footprint_bytes`, `webkit_malloc_bytes`, `owned_graphics_bytes`, `gigacage_bytes`, and the workload: `sessions`, `agents`, `terminals`, `panes_drawn`, `desktops`, `seeds` |
| `daemon_starts` | daemon start | `daemon_run`, `previous_end`, `stop_reason`, `previous_run` |
| `app_actions` | app action | `action` (the record's name), `id`, `section`, `reason`, `to`, `wait_ms`, `kind` |

A change to a record's fields changes its view in the same commit, and the
service drops and recreates the views on every start, so the ClickHouse user
needs `CREATE VIEW` and `DROP VIEW`. The views run with the service's rights
(`SQL SECURITY DEFINER`), so a user granted `SELECT` on the views reads them
without any grant on `events`.

## Limits

Every refusal names the limit, gives the limit's value, and gives the value that
this request would produce. A `429` also says when to retry:

```json
{"limit": "events_per_install_per_minute", "value": 10000, "observed": 10200, "retry_after_s": 41}
```

| Limit | Value | Status | Receipt |
|---|---|---|---|
| `events_per_install_per_minute` | 10,000 in any 60 s | 429 | measured peak minute 882 |
| `events_per_install_per_day` | 1,000,000 in any 24 h | 429 | measured peak day 87,414 |
| `bytes_per_install_per_day` | 128 MiB uncompressed in any 24 h | 429 | a redacted peak day is 10.9 MB uncompressed, about 11× under the limit, the same margin as the event limits |
| `bytes_per_day` | 2 GiB uncompressed in any 24 h, across all installs. Set with `ATTN_INGEST_BYTES_PER_DAY` | 429 | guessed: about 16 installs at their peak budget. Remeasure from real traffic |
| `installs_per_ip_per_hour` | 20 in any hour | 429 | guessed. Remeasure from real registrations |
| `records_per_batch` | 10,000 | 413 | the minute limit. A larger batch could never be accepted |
| `request_body_gzip_bytes` | 4 MiB | 413 | a redacted peak day is 613 KB gzipped |
| `request_body_bytes` | 16 MiB | 413 | 10,000 redacted records are about 1.25 MB uncompressed (125 B each) |
| `build_line_bytes` | 2 KiB | 413 | a release build's line is 313 B, and every stored row repeats it |
| `record_line_bytes` | 64 KiB | 413 | guessed: 4× the 16 KB vent text the vent tool will allow. Remeasure from the largest real line |

Records and bytes count against the event and byte limits whether or not they
turn out to be duplicates. A refused batch counts nothing. The service handles the
batches and the delete of one install one at a time, so concurrent retries count as
duplicates and a delete leaves nothing behind. The service decodes at most 2
batches at once across all installs, and the other batches wait. A batch at
the 16 MiB limit took the service to 130 MiB resident (measured 2026-10-24),
while a real batch of 10,000 records is about 1.25 MB. The counts live in memory
and reset when the service restarts. Read and write timeouts are 2 minutes,
because 4 MiB at 1 Mbit/s takes 32 s.

Every stored row keeps a keyed hash of the address that sent it
(HMAC-SHA256 with `ATTN_INGEST_IP_SECRET`), never the address.

## Quarantine

To refuse an install or an address, insert the install id or the address hash
into the `quarantine` table. The service then refuses that install or address
with `403` and the reason, on registration and on every batch. To remove the stored events of a
quarantined install, run the `DELETE`:

```sql
INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('i-…', '', 'flooded garbage names', now64());
DELETE FROM events WHERE install_id IN (SELECT install_id FROM quarantine WHERE install_id != '');
```

## Configuration

| Variable | Meaning |
|---|---|
| `ATTN_INGEST_CLICKHOUSE_URL` | Required. ClickHouse's HTTP interface with credentials and the database, such as `http://user:password@clickhouse:8123/?database=attn`. The service creates its tables and views there on start. |
| `ATTN_INGEST_IP_SECRET` | Required, at least 32 bytes. Rotating it stops new rows' address hashes from matching older ones. |
| `ATTN_INGEST_LISTEN` | Listen address, `:8080` by default. |
| `ATTN_INGEST_BYTES_PER_DAY` | The `bytes_per_day` limit in bytes, `2147483648` (2 GiB) by default. |
| `ATTN_INGEST_CLIENT_IP_HEADER` | The header that carries the client address behind a proxy, such as `CF-Connecting-IP`. If unset, the service uses the connection's address. Set it only where the proxy is the only way in. |

The service serves `/metrics` and `/healthz` on the same address as the API.
Expose only `/v1/` publicly.

## Image

`.github/workflows/ingest-image.yml` publishes
`ghcr.io/solenesinc/attn-ingest:sha-<short>` for every push to `main` that
changes the service, and on demand. The image holds only the static binary and
runs as a non-root user. The deployment pins one of those tags.

## Running locally

```bash
make ingest-clickhouse            # starts ClickHouse 25.8 in Docker and prints its URL
ATTN_INGEST_CLICKHOUSE_URL='http://attn:attn@127.0.0.1:18123/?database=default' \
ATTN_INGEST_IP_SECRET=0123456789abcdef0123456789abcdef \
ATTN_INGEST_LISTEN=127.0.0.1:8080 go run ./cmd/attn-ingest
make ingest-clickhouse-stop
```

An instance other than production uploads nowhere by default. Start its daemon
with `ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:8080` to upload to this local
service. Only the default instance on the default data folder uploads to
`https://attn-telemetry.caketroll.cloud` without the variable.

## Testing

The stack tests in `cmd/attn-ingest` and the daemon's end-to-end upload test,
`TestSharingUploadsTheOutboxToAttnIngestUntilTheUserForgetsTheInstall`, run
against a real ClickHouse, each test in its own database, through
`testworld.NewClickHouse` and `testworld.StartIngest`. The other analytics
upload wire tests in `internal/daemon` run against `testworld.StartFakeIngest`,
an in-process server that keeps attn-ingest's HTTP contract.

`ATTN_TEST_CLICKHOUSE_URL` names the ClickHouse to use. Without it, the first
test in each test process runs `scripts/ingest-clickhouse.sh start`, which
starts the same container as `make ingest-clickhouse`, and later runs reuse the
container. Stop the container with `make ingest-clickhouse-stop`. Nothing starts
OrbStack or the Docker daemon. When no Docker daemon answers, the tests skip and
name what is missing. Under CI, or with `ATTN_TEST_REQUIRE_CLICKHOUSE` set, the
tests fail instead. `scripts/landing-tests.sh` sets that variable. The landing
tests' `clickhouse` group runs the tests when a landing touches the analytics or
ingest code. Go's test cache cannot see the ClickHouse server, so
`scripts/landing-tests.sh` names every ClickHouse test and runs it with
`-count=1`; add a new one there.

## ClickHouse memory

Measured on 2026-10-24 with ClickHouse 25.8.33.6 in Docker under a 1 GiB
limit. The load was 87,414 redacted rows built from the busiest day of a copy
of a production database (2026-09-27, 87,413 facts), followed by per-hour and
per-subject aggregates:

| | Resident | cgroup peak |
|---|---|---|
| Empty | 420 MiB | 467 MiB |
| Peak day loaded | 466 MiB | 597 MiB |
| After queries | 505 MiB | 658 MiB |

The day takes 697 KiB on disk (10.2 MiB uncompressed). Most of the memory is
ClickHouse's own baseline, not the data.
