# Database migrations

- Create a SQL migration from the repository root:

  ```sh
  go run ./cmd/db-migrations new session_priority
  ```

  The command creates `internal/store/migrations/<unix-microseconds>_<slug>.sql`
  with the current Unix timestamp in microseconds (one million slots per second).
  Use lowercase words separated by underscores.
  If the timestamp is already used in this checkout, the command names the file
  and refuses to overwrite it; rerun to get the current timestamp. Existing
  14-digit UTC-second IDs remain supported. Write SQL in the empty file.
- Write plain SQL. Each migration runs once; do not add existence guards to
  accommodate another branch's schema.
- Use Go only for data logic SQL cannot express:

  ```sh
  go run ./cmd/db-migrations new --go backfill_seed_profiles
  ```

  This creates `internal/store/migration_<timestamp>_<slug>.go` with its
  registration. Implement the generated function; its placeholder returns an
  error until replaced.
- Never edit or rename a merged migration. Never change the frozen legacy
  ladder, its migration definitions or their reachable package-level helpers and types.
  `make check-migrations` compares them with `origin/next` and checks that merged SQL and Go files retain their paths
  and contents. CI uses the PR base or the previous push as its baseline.
- Run `make generate-schema` and commit `internal/store/schema.sql` with the
  migration. CI runs `make check-schema` to check that it is current.
- A `schema.sql` conflict on rebase means another PR touched the same table.
  Read both migrations and make yours correct on top of theirs. Give your
  file a fresh timestamp if it must run after theirs, then regenerate the dump.
- Prefer `ALTER TABLE` to rebuilding a table. A rebuild copies columns from
  the live table rather than a hard-coded list.
- Delete and recreate a dev database that ends up odd after switching branches.
