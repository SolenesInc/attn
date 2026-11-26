Aligns the desktop and queue sidebars with the title bar shelf's tonal language and four-tier state marks.

- **Current desktop tonal well**: the active desktop sits in an elevated well (`bg_elevated` + hairline border) and its active tile sits on a raised `bg_selected` pill with `border_hover` instead of a left brand stripe.
- **Shelf state marks and luminance hierarchy**: agent rows share `attn_ui::blocks::state_mark` with the shelf (filled circle when waiting, rounded square on approval, tinted wash while working, bare icon when idle) and use three-tier title contrast.
- **Quieter desktop rows**: drops synthetic `10+` chips on unslotted named desktops, folds the `Desktop overview` trigger into the `DESKTOPS` heading rule (`overview ⌘G`), simplifies automation provenance to a compact `⚡` mark, and drops desktop-flow priority/delegation accent pills while keeping monochrome role glyphs (`✦`, `</>`) and queue-flow priority flags.

![Desktop sidebar dark](https://land-feed.caketroll.cloud/m/SolenesInc/attn/ac82333a16fe219cc56d53925e630d680d81f47804a29a5ea837be67a0eb6e12.png)
![Desktop sidebar light](https://land-feed.caketroll.cloud/m/SolenesInc/attn/eb3afd23c66acc5c657757c7483b588be7a15d56ebd735ab44b5f9511bcfd161.png)
