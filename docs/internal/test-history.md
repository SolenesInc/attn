# Test history

`go tool test-history` (`internal/testhistory/`, `scripts/test-history/`)
captures receipts (`attn.test-receipt/v1`), syncs them with the private tailnet
service `https://attn-test-history.int.caketroll.cloud`
(`SolenesInc/attn-test-history`), selects landing tests, and generates flake
reports.

## Commands

```sh
go tool test-history sync                                     # upload local receipts, update mirror
go tool test-history select                                   # preview landing selection for this checkout
go tool test-history explain --test 'go ./pkg TestName'       # history, lane status and cost
go tool test-history report                                   # flakes, unresolved failures and fixed waits
go tool test-history replay --from <rev>                      # replay selection over main history
go tool test-history spool                                    # print shared spool directory path
go tool test-history recover                                  # seal receipts from killed runs
```

- Bare `go test` and `cargo test` record nothing; `scripts/test-go.sh`,
  `scripts/landing-tests.sh`, `real-app:serial-matrix` and CI's `Native` jobs
  capture receipts automatically.
- `ATTN_LAND_BASELINE=1 land` skips history sync and selection and runs the
  baseline groups in `scripts/landing-tests` alone. Use only when test history
  or selection misbehaves, never to skip a failing test.
- `ATTN_TEST_HISTORY_URL` overrides the service URL.

## Provisioning the service token

`sync` reads `${XDG_CONFIG_HOME:-~/.config}/attn-test-history/token` from
Proton Pass (Personal vault, `attn-test-history ingestion`):

```sh
dir="${XDG_CONFIG_HOME:-$HOME/.config}/attn-test-history"
mkdir -p "$dir"
(umask 077; pass-cli item view --vault-name Personal \
  --item-title 'attn-test-history ingestion' --field password > "$dir/token")
```

## Traps

- **Tests Go's cache cannot invalidate:** `scripts/landing-tests.sh` allows Go
  test caching across worktrees. Any test whose inputs live outside its Go
  package (such as building another binary or talking to an external server)
  must be listed in `scripts/landing-tests.sh` so it runs with `-count=1`.
- **Schema changes:** `internal/testhistory/receipt.go` owns the schema. If you
  change a field's meaning, bump the schema version. After a Go or Cargo runner
  upgrade, regenerate fixture output with
  `internal/testhistory/testdata/regenerate.sh`.
- **Lane clearing:** Only fresh (non-cached) passes in the failing lane clear a
  failure (`testhistory.ClearedAfter = 30`). Selection ignores other-OS lanes
  (and macOS scenario failures, since landings run scenarios on Linux).
