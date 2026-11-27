On macOS a web link in an ask now opens in a tab of the Asks panel, on a web view the panel owns, instead of the system browser, so you read it beside the question. The desktop's browser tile is never navigated. Linux keeps the system browser.

The browser host is now keyed by page (a tile, or the Asks panel), so the panel's page reuses the tile pages' engine and keys. The Asks sheet always holds focus, so the panel's page keeps the keyboard from a click in it until the sheet's focus moves or the sheet is pressed.

Before, the browser tile (macOS VM):
![tile before](https://land-feed.caketroll.cloud/m/SolenesInc/attn/6d21c6a9771c05e686d4c93efb94018f1c782b9bca18db325fdbf1ae73ba1ba1.png)

The ask's link opens in the panel, tab named by host:
![panel](https://land-feed.caketroll.cloud/m/SolenesInc/attn/5435a2f86ff092c66d56e8f46d8b0aed39a88b0a6dcb84a329af2fbc16886493.png)

Asks closed, the tile still shows its page:
![tile after](https://land-feed.caketroll.cloud/m/SolenesInc/attn/1d732cc8ef5ea5fdf88cea190726999e9582b1c8d3821d1b45d7821cd0cf1c0d.png)
