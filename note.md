# Slice 3: Uncapped open-only live push, Crew Planted paging, and on-demand closed seed resolution

## Summary
- **Daemon live push (`internal/garden/window.go`, `internal/daemon/garden.go`)**: Switch `seedsForBroadcast` from the 1,000-seed capped `garden.Window` to `garden.LiveWindow`, which pushes every open seed, every open session's stake (even once harvested or withered, until the session closes), and their ancestor plots with no numeric cap. `attn seed ls` retains its 1,000-seed window.
- **Crew → Seeds Planted (`native/crates/attn-app/src/crew/{seeds.rs,view.rs,mod.rs}`)**: Page a member's planted seeds (`status: "all"`) via `garden_seeds_page` with virtualized rows, find-field daemon filtering, O(1) parent-plot lookup, signature-gated refreshes so unrelated garden pushes skip redundant fetches, and head-merge past 200 rows so deep scroll positions and tail cursors are preserved on refresh. Removed the cap banner.
- **On-demand closed seed IDs & titles (`native/crates/attn-app/src/{terminal/ids.rs,desktop/seed_card.rs,hints.rs,garden/checks.rs,sidebar/,desktop/,palette/}`)**:
  - Resolve closed seed IDs referenced in terminals and markdown on demand via `garden_seeds_lookup` (batched, bounded cache, cleared on disconnect/profile switch, pruned when live), feeding hover cards, Cmd+L hints, and link opens.
  - Resolve closed inheriting children in plot `shared_checks` from `SeedDocument.related` when absent from the live open push.
  - Keep seed tile titles intact in sidebar rows, desktop tile headers, shelf, overview, peek, palette, and detached windows after a seed is harvested or withered by falling back to the cached `SeedDocument`.
- **Feature map (`garden.md`, `markdown.md`)**: Updated performance/idle cost and `markdown-ids` entries to reflect uncapped open-only push, live-session stakes, Crew Planted paging, `garden_search` vs local `is:` lens filtering, and `garden_seeds_lookup`.

## Verification
- **Live 1,607-seed daemon receipt** (50 open + 1,557 closed seeds via `phase1_receipt.go`):
  - Before (`garden.Window`, 1,000 cap): `initial_state` = 640,566 B (12.7 ms), `pushed=1000`, `total=1607`.
  - After (`garden.LiveWindow`, uncapped open-only): `initial_state` = 43,189 B (4.3 ms) — 14.8x smaller, 3x faster — `pushed=50`, `total=1607`, exact `closed_counts`.
- **Go**: `./scripts/test-go.sh --packages -v -race -count=1 ./internal/garden`, targeted `TestGardenPush*` / `TestEverySeedMoveReachesTheAppAsOneGardenPush` / `TestTheGardenIsReadWholePastOnePage` / `TestGardenSeedsPage*` / `TestGardenSearch*` in `./internal/daemon`, and `go vet ./internal/garden ./internal/daemon` — all passed.
- **Native Rust**: `cargo fmt --all --check`, targeted `attn-app` wire tests (`garden`, `crew`, `terminal_ids`, `markdown_ids`, `hints`, `sidebar`, `shelf`, `overview`, `peek`, `palette` — 696 passed after fixing the new Crew >200 test fixture and re-running `crew` + `garden::tile`, 115/115 passed), `cargo clippy --locked -p attn-client -p attn-ui -p attn-app --all-targets -- -D warnings`, `scripts/native-clippy-allows`, `scripts/native-host-reads`, and `TMPDIR=/tmp make check-types` — all passed.
- **Pre-landing reviews**: Ran `verify-docs`, `verify-perf`, `verify-tests`, and adversarial reviews; fixed all confirmed findings (feature-map accuracy, Crew Planted signature-gated refresh + virtualization + non-truncating >200 head-merge + O(1) parent lookup, `tile_title` fallback for closed seeds, and `AwaitSince` frame pinning in `TestGardenPushPinsLiveSessionDispatchCrownAfterHarvestUntilSessionCloses`).
