# attn-pi plugin guide

**Read pi's own docs before changing this plugin.** The set for the pinned
version is installed at `node_modules/@earendil-works/pi-coding-agent/docs/`
(`bun install` here if it is missing): `extensions.md` for the extension API,
the event lifecycle and session replacement; `sdk.md` for the headless runtime
nisse's host drives; `session-format.md` for the JSONL and `SessionManager`;
then `rpc.md`, `settings.md`, `compaction.md`, `tui.md`, `models.md`.

## Auto mode

`automode/` is attn's pi extension for an automated permission system: a user-configured static
pre-approved or denied list, plus a classifier for everything reaching past it, denied
conversationally rather than through dialogs.
