Slice 2 of removing the 1,000-seed Garden UI cap: native Garden List/Board closed paging, daemon-backed search, any-seed trail via SeedDocument.ancestors, and removal of the List and Board cap banners.

What changed:
- Added `full_push` generation tracking to `attn_client::store::Garden` so closed page buckets refresh on reconnect/full snapshot without refetching on unrelated live-seed updates.
- Added on-demand closed history paging (`garden_seeds_page`) to Garden List (v1 root, v1 wide columns, v2 root, plot children) and Board (expanded Closed column with virtualized cards, exact folded counts from `garden_closed_counts`, and replant deduplication).
- Switched Garden search to daemon-backed `garden_search` across the full garden (including archived logs and outside-plot/hidden-closed counts) with pagination, out-of-order response discarding, and error/retry preserving the failed cursor/offset.
- Unlocked any-seed navigation and breadcrumbs from `SeedDocument.ancestors` and `SeedDocument.related`, cached pre-wrapped `Arc<Seed>`s on `Reading`, and fixed v2 bed/lens alignment on archived hits.
- Removed the List and Board 1,000-seed cap banners.

Pre-landing review findings addressed (Verify Tests, Verify Perf, Adversarial):
- Eliminated per-paint `Seed` deep clones (`Reading` cached `Arc<Seed>`s), eager `RowLook`/`CardLook` building outside `virtual_rows`, and `Index::of` on unrelated board events.
- Preserved loaded page/search depth on refresh, evicted stale page buckets, cleared caches on panel close, fixed archived plot drill-in on Board and v1 wide columns, kept folded Closed summary during Harvest/Wither composition, and simplified `living_trail` to avoid erasing connection errors.
- Expanded Go (`TestGardenSearch`) and Rust wire tests (30-80 item virtualized paging, offscreen sentinel `runtime_is_at_rest`, error/retry preserving cursor/offset, out-of-order search discard, replant dedup, archived plot drill-in, v2 bed/lens alignment, archived tile relations).

Checks run:
- `./scripts/test-go.sh --packages -race -count=1 -run 'TestGardenSearch' ./internal/daemon` (pass)
- `cargo fmt --all --check` (pass)
- `cargo test -p attn-app --test wire garden` (251 passed)
- `cargo test -p attn-app --lib garden::search` (8 passed)
- `cargo clippy --locked -p attn-client -p attn-ui -p attn-app --all-targets -- -D warnings` (pass)
- `scripts/native-clippy-allows` and `scripts/native-host-reads` (pass)
