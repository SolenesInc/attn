A kept view can now keep things: `:::data store="items"` gives it a store of items that its tile changes with `add`, `set`, `toggle` and `remove` (no start key, no program runs), and that an agent reads and changes with `attn view data list|add|set|remove` (the `view_data_*` tools). Items live in the home's database, so they survive restarts and a home move. Deleting the view deletes its stores and says so. Behind `views_enabled`.

An `:::entry` now takes a letter like a control does (A for `add` in the to-do view), and the keyboard stays in the line after ↩, so a to-do list works by keys alone:

![To-do list used by keys alone](https://land-feed.caketroll.cloud/m/SolenesInc/attn/2a6e82031c3f9ad746b1e5e96d8697dc66c0a52ef7ddd002742fa5652fc20e89.mp4)
