Replace the blind retry escape hatch for flaky landing tests with paired diagnosis and owned tracking.

- `scripts/landing-tests.sh` now prints a paired-repro command (`-count=10 -race` for Go) and `go tool test-history explain --test '<unit>'` instead of a single `-count=1` rerun, with matching coverage in `scripts/land_test.sh`.
- `land-on-main`, `sheriff`, `attn-workflow` (`implementation.md`, `orchestration.md`), `verify-tests`, and `review-main/finding.md` now require paired `HEAD` vs `origin/main` checks before acknowledging a flake, dispatch fixes on the first unowned flake instead of waiting for a repeat, and flag flake-silencing test weakenings as P1.
