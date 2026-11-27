Brought the desktop flow sidebar polish to queue flow (both the queue sidebar and the collapsed queue bar):

- **Shared Crew pills**: both flows now share the compact Crew pill bar (`sidebar.crew`), with a quiet `•••` menu on resting pills too so member details and Chief setup/charter actions stay reachable in either state (and `Esc` disarms an armed start pill).
- **Tonal waiting well & inline counts**: owed turns sit inside a tonal waiting well (`queue.waiting-well`) that dissolves to flat `Nothing owed` when empty, and the closed agent-list toggle shows working/snoozed counts inline (`▸ N MORE  working · snoozed`).
- **Flat Automations section**: replaced collapsed per-automation folders in the queue sidebar with the unified flat `AUTOMATIONS` section (`● N waiting` pill, `all` drawer button, active runs directly visible, finished off-screen runs folded). Automation delegates stay in the main queue like regular delegates.
- **Filtered desktop chips & tonal bar pills**: user desktop strips and the collapsed queue bar now omit unslotted automation desktops and non-current empty ephemeral/crew desktops, use tonal `+N` chips, put `overview ⌘G` in the strip footer, and show the collapsed runs chip with `⚡` in an amber-washed pill when runs need you.

![Queue sidebar with tonal waiting well, Crew pills, inline counts, and desktop strip (dark)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/8183e2bf7fcba2b85c17c8e1becc9239d7993aae29791c6e0d71ff342b3ca97d.png)
![Queue sidebar with tonal waiting well, Crew pills, inline counts, and desktop strip (light)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/bff38f2ae0ed950555a82d3484376f61a69ae4ca7472e4ac5ea62c18a234157b.png)
![Queue sidebar with dissolved empty well and flat Automations section (dark)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/1a91749d8a67d5d8762952f33eba07e5b4c3ef1c802fb76839acb4cbb4a5ab72.png)
![Queue sidebar with dissolved empty well and flat Automations section (light)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/ed85c6c0ae1c596535d76de4cbf2973b7d70f63c0fbbd34e69c8266139943341.png)
![Collapsed queue bar with tonal ⚡ runs pill and peek popover (dark)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/6b5e4d4c836277b2e3c188dd62d57504b989f90fd5ecec6b124c1c96ca618e1c.png)
![Collapsed queue bar with tonal ⚡ runs pill and peek popover (light)](https://land-feed.caketroll.cloud/m/SolenesInc/attn/0ee08149d33c6594e0148294ff995a639f08b343c1ccbcfe305f1ca58128c2dd.png)
