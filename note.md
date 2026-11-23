`attn` now lands on `main` with the `land` CLI (`0.10.0+`), publishing landings to `https://land-feed.caketroll.cloud/SolenesInc/attn`.

- Added `land.toml` wiring `gate = "scripts/land-gate.sh"`, `resolve = "scripts/land-resolve.sh"`, `record = "scripts/land-record.sh"`, and `feed = "https://land-feed.caketroll.cloud"`.
- Retired `scripts/land.sh` as a non-zero pointer that guides agents and developers to `land`, `land guide`, and `ATTN_LAND_BASELINE=1 land`.
- Updated `.githooks/pre-push` to gate on `LAND_LANDING=1`, adapted `scripts/land_test.sh` and `scripts/measure-verification-cost.py`, and updated `AGENTS.md`, `.agents/skills/*`, and `docs/internal/*`.
