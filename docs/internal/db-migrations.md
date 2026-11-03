# Database migrations

attn refuses to open a database containing an applied migration the build
does not support. This applies to daemon startup and CLI commands that open
the database directly. Install a build that supports the database's migrations,
or restore a backup taken before the upgrade with `attn db restore`.

- Create a SQL migration from the repository root. Use lowercase words
  separated by underscores for the slug:

  ```sh
  go run ./cmd/db-migrations new session_priority
  ```

  The command creates `internal/store/migrations/<unix-microseconds>_<slug>.sql`
  with the current Unix timestamp in microseconds. The timestamp has one million
  slots per second. Write your SQL in that empty file. If this checkout already
  uses the timestamp, the command names the file and refuses to overwrite it.
  Rerun the command to get the current timestamp. Existing 14-digit UTC-second
  IDs remain supported.
- Write plain SQL. Each migration runs once, so do not add existence guards to
  accommodate another branch's schema. Write schema that the system SQLite on
  every supported platform can read. That SQLite can be older than the SQLite
  bundled with the Go driver. Packaged-app checks also exercise the system
  SQLite.
- Use Go only for data logic SQL cannot express:

  ```sh
  go run ./cmd/db-migrations new --go backfill_seed_profiles
  ```

  The command creates `internal/store/migration_<timestamp>_<slug>.go` with its
  registration. Implement the generated function. The placeholder returns an
  error until you replace it.
- Never edit or rename a merged migration. Never change the frozen legacy
  ladder, its migration definitions, or their reachable package-level helpers and types.
  `make check-migrations` compares all of these with `origin/main` and checks that
  merged SQL and Go files keep their paths and contents. CI uses the PR base or
  the previous push as the baseline.
- Run `make generate-schema` and commit `internal/store/schema.sql` and
  `internal/store/schema.sqlite.gz` with the migration. CI runs
  `make check-schema` to check that both are current. A new database starts as
  a copy of `schema.sqlite.gz`, the database that replaying every migration
  makes, and then gets its own Default profile id and timestamps. While the
  file is behind the migrations, every new database replays them all instead.
- A `schema.sql` conflict on rebase means another PR touched the same table.
  Read both migrations and make yours correct on top of theirs. Give your
  file a fresh timestamp if it must run after theirs. A conflict on
  `schema.sqlite.gz` comes with any other migration. In both cases, run
  `make generate-schema` again.
- A migration changes every row it matches, including the rows an outpost
  keeps as read-only copies of its home's. The home runs the same migration on
  its own rows, so the copy stays in step.
- Prefer `ALTER TABLE` to rebuilding a table. When you rebuild a table, copy
  the columns from the live table rather than from a hard-coded list.
- Delete and recreate a dev database that behaves oddly after you switch branches.
- A migration that rewrites document text or metadata held in a text index must rebuild that collection's index.
