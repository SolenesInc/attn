Makes default own launch desktops ephemeral and unifies automation runs into a collapsible desktop-sidebar section.

- **Ephemeral own launch desktops**: default unslotted own launch desktops (`desktops.ephemeral`, `Desktop.ephemeral`) are created lazily on first run and reaped 30 seconds after becoming empty and non-current, then recreated lazily on the next run; custom-named, slotted, shared, and user-renamed desktops stay permanent.
- **Unified desktop `AUTOMATIONS` section**: unslotted automation-only desktops fold out of `DESKTOPS` into a single `AUTOMATIONS` section (`all` opens the Automations drawer; finished off-screen runs fold behind a disclosure; waiting and active runs stay visible), collapse to one `⚡` chip on the narrow rail, stay out of `⌘G` desktop overview, and remain searchable in `⌘K`.
- **Desktop chief setup & acceptance harness**: anchors `chief-setup` beside the desktop sidebar and dismisses it when modal layers open, updates `DaemonObserver.closeDesktop` across scenarios so teardown handles a reaped empty starting desktop cleanly, and updates reference docs and feature maps.

![Desktop sidebar automations section dark](https://land-feed.caketroll.cloud/m/SolenesInc/attn/d6232d247075dd0c5534582911355ed855b0932c5b25caad38fd8e62a1bfc0f3.png)
![Desktop sidebar automations section light](https://land-feed.caketroll.cloud/m/SolenesInc/attn/e43db30a0fb27ce322f8e61e78f78c9b9063404821a808c67d910ebc79e89b99.png)
