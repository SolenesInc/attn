# The Tauri app's localStorage, as WebKit keeps it

This folder stands in for `~/Library/WebKit/<bundle id>`, the Tauri app's web
data on macOS. Its layout, `origin` file and table match a store copied from a
named instance's Tauri app on 2026-11-12 (WebKit on macOS 26).

```
WebsiteData/Default/<salt>/<salt>/origin
WebsiteData/Default/<salt>/<salt>/LocalStorage/localstorage.sqlite3
```

`<salt>` is a hash WebKit derives per origin; here it is a readable name. The
`origin` file is WebKit's serialized origin, `tauri://localhost` for the page
and its top frame. The database runs in WAL mode in a live store and has one
table:

```sql
CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB NOT NULL ON CONFLICT FAIL);
```

Each value is the `localStorage` string encoded as UTF-16LE. The fixture holds
all 12 settings the Tauri app writes for itself, plus `attn.grid.layout`, a key
an older build wrote that the import must leave behind.
