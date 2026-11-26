# Shelf-unified sidebar polish and automation delegation-chain grouping

## What changed

- **Removed horizontal divider rules from `GroupHead`**: section headings (`CREW`, `DESKTOPS`, `AUTOMATIONS`, `DOCK`) and desktop headers no longer stack adjacent hairlines or bisect desktop names/numbers and shortcuts.
- **Aligned desktop rows on one vertical axis**: `ListRow::well` now hangs by `Space::S4` to match its `Space::S4` inner padding over a calmer tonal well surface (`ListRow::well_surface`) with `Radius::Panel` corners, eliminating the 4px horizontal wobble when switching current desktops.
- **Matched the title bar shelf's tonal language**: inactive desktop slot chips use soft borderless tonal pills, crew and empty-desktop pills use capsule geometry (`Radius::Round`, quiet resting pills borderless), the `Automations` waiting counter uses a warning-washed capsule button (`system::pill_button`), and sidebar tile kind labels (`DOC`, `SEED`, `WEB`, `NOTE`) use quiet borderless tonal tags (`system::quiet_tag`).
- **Kept automation delegates unified under Automations**: an unslotted desktop holding an automation run and agents delegated from it (or shells split from them) now stays grouped under `Automations`, out of `Desktops`, `⌘G` overview, and the collapsed rail's desktop chips. Waiting automation delegates can be settled from their row `✓`, `⌘⇧E`, or the command palette, and walked via `⌘⇧J`, the `N waiting` chip, and the rail `⚡` chip in desktop flow (without starving waiting runs on numbered desktops). Adding an unrelated agent/tile or closing the automation root still promotes the desktop back into `Desktops`.

## Verification

![Desktop sidebar in dark mode](https://land-feed.caketroll.cloud/m/SolenesInc/attn/20e6e85d78372b7ff3e749d9e53838531319d8f660e2438afe832406e91f0bfa.png)

![Desktop sidebar in light mode](https://land-feed.caketroll.cloud/m/SolenesInc/attn/c6ae75aa5903051483d5ec8213eca1e4cc2530ea22828505e0e2952abfc20ced.png)
