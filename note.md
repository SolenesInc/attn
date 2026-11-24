# Garden v2 Step 4 — Native List, Board, and Dock UI behind `garden_v2_enabled`

## Summary

Adds the Garden v2 native UI behind the toggleable `garden_v2_enabled` experimental setting, leaving v1 unchanged when off:
- Fullscreen List: 3-pane layout (`Beds` rail with `+`, waiting dots, hover edit, `Unassigned`, and `Automations` separated out of `All` | virtualized 2-line pulse list grouped by `Waiting on you` and `In flight` / `Needs a decision` / `Open` | Seed Reader with Bed pill, pulse strip + live-session jump, and plot children).
- Fullscreen Board: left `Beds` rail (or compact chips on narrower windows), `In progress` ordered by live attention priority, calm 2-line pulse cards, and `b` quick-assign.
- Dock & narrow windows: compact Bed chips with semantic waiting dots, `Now` / `All` toggle, and virtualized pulse rows.
- Overlays & shortcuts: `Dialog` for adding/editing/removing beds (`garden_bed_save` / `garden_bed_delete`), keyboard-navigable `BedPicker` (`b` shortcut or root Bed pill, arrows + Enter + Escape, resolving child seeds to their root plot, ignoring automations), `bed:` search operator with `Partial::Bed` autocomplete and pulse snippet matching, and `j`/`k` navigation.
- Updated `garden_v2_enabled` setting copy, `.agents/skills/drive-attn/features/garden.md`, `docs/internal/native-app.md`, and `screenshot_tour.rs` scenes (`garden-v2-{list,board,dock}`).

## Verification

- `make check-native`: PASS (`native-clippy-allows`, `native-host-reads`, `cargo fmt --check`, `cargo check --workspace --all-targets`, `cargo clippy --workspace --all-targets -D warnings`, UI budget).
- `TMPDIR=/tmp make check-types`: PASS.
- `cargo test -p attn-app --lib garden::search`: 20/20 PASS.
- `cargo test -p attn-app --test wire -- garden`: 230/230 PASS (including 8 new `wire::garden::v2` tests covering flag-off v1 preservation, virtualization & 1-row/1-card rebuilds, bed CRUD, `b` keyboard picker, child-ask bubbling, `bed:` search, and live-session jump).
- `RUNE_PLATFORM=linux cargo test -p attn-app --test wire -- wire::garden::v2`: 8/8 PASS.
- `./scripts/native-screenshot-tour shoot --only 'garden-v2*'`: 6/6 dark+light PNGs visually inspected; 0 clips, 0 overlaps.

## Pre-landing Review

3 independent read-only reviewers ran on the Step 4 diff (`Verify-Skills`, `Correctness`, `Machinery & UI`). All confirmed P1 findings were fixed and re-verified before landing:
- Virtualized `v2_root_list` with lazy per-row `V2RowLook` and moved `scoped_seeds` inside `if query.searches` with zero-allocation `v2::root_id`.
- Fixed `tiles.area` memo key for v2 seed tiles on Ask/session updates, mounted `garden::overlay` in popped-out tile windows, routed `pulse_strip` session jump with `close_garden` + `raise_main()`, and added capped-out seed fallback in `open_bed_picker`.
- Blocked global shortcuts in `keys::modal`, handled `Key::Enter` in `BedPicker`, reconciled `open_seed`/`open_found` away from Board and mismatched Bed/Lens, reset v2 state on profile switch/flag toggle, gated `bed:` search operator on `garden_v2_enabled`, and excluded automations from `bed:unassigned`.
- Fixed wide-v2 `closed_toggle` counts, plot-child `walkable_rows` focus, board footer shrinkage, and duplicate `"Garden"` board plot trail.
- Upgraded `lens_switch` to `Segmented`, `bed_chip`/`dock_lens_button` to `Button::chosen` + semantic waiting dot, `BedForm` to `Dialog`, deduplicated `model::short_age` & `v2::Snapshot`, strengthened wire tests, and updated `.agents/skills/drive-attn/features/garden.md`.
