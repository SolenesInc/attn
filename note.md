The Asks panel's tabs now read like the title bar's shelf: each tab is a shelf entry with an icon for its kind (seed, markdown, web, terminal), the shown tab is drawn as the current entry, a terminal tab carries the shelf's working mark while a command runs, and tabs that do not fit fold into `‹ n` / `n ›` counts at the ends (a press shows the nearest folded tab). The shown tab never folds.

The shelf's fold layout moved from the app's desktop shelf into `attn_ui::blocks::ShelfRow`, so the title bar and the panel share one layout instead of two copies.

![Panel tabs under the shelf, macOS VM](https://land-feed.caketroll.cloud/m/SolenesInc/attn/45350773e5da04d79958e383e70c169e1cb5e289258902acfe7313fb2a982f50.png)
![Folded at both ends, macOS VM](https://land-feed.caketroll.cloud/m/SolenesInc/attn/afed5549a1920e16f72cdb097140eb0346d9e90154e4839539178d3c56940b54.png)
![Folded at both ends, Linux](https://land-feed.caketroll.cloud/m/SolenesInc/attn/3161e32f6a3b07e2ed7253d2a98fb73279c579c31f1aed66cc037258d47ffe1b.png)
