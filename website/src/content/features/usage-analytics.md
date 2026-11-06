---
title: Usage analytics
summary: Attn can record how it is used on your machine and, if you choose, share that record with Attn’s maintainers. Both are off by default.
order: 7
guide:
  - link: telemetry
    label: Telemetry, the full guide
  - link: telemetry#what-is-recorded
    label: What is recorded
  - link: telemetry#what-is-sent
    label: What is sent
  - link: telemetry#keeping-and-deleting
    label: Keeping and deleting
---

## What it does for you

With recording on, **Settings → Analytics** shows every record, exactly as it is stored and sent. New sessions also get `attn vent`, so your agents can file a short report when Attn gets in their way.

With `analytics.enabled` off, nothing is recorded and nothing is sent, regardless of the other settings.

## What stays on your machine

Attn redacts records on the `bus`, `app`, `cli`, `sample` and `run` streams before it stores them in the local outbox:

- A subject, such as a session, path or pull request, becomes a keyed hash. The secret for that hash never leaves the machine.
- A string becomes its length, unless Attn classifies its field. A field whose values come from a fixed set, such as a status or a kind, is kept as it is, and a field that holds an id becomes the same keyed hash.
- Numbers, booleans and nulls are kept.
- Repository facts carry three booleans: has an `AGENTS.md`, is a worktree, has a remote. Names and paths never leave the machine.

Tool vents, which are about the agent's other tools, never leave the machine. Neither do a vent's session title, machine, repository, worktree, stake seed or transcript.

Attn deletes records from the outbox 7 days after it writes them. While sharing is on, a record not yet sent stays up to 30 days.

## What sharing sends

While sharing is on, Attn uploads to `https://attn-telemetry.caketroll.cloud` every 5 minutes, and once right away when you turn sharing on. Only the default instance on the default data folder uploads there.

- **Records.** Every fact on Attn's event bus except `vent.*` facts, shortcuts and palette commands used, Settings sections opened, queue advances, the kind of a UI error, one record per `attn` command, one record per agent run with its harness, model and tokens, and samples of the daemon's memory, CPU time and sizes.
- **Build details.** The version, commit, channel, build time, operating system and architecture of the build and the app that wrote the records.
- **An install token.** The first upload registers the install. The token identifies the install, not you. The server also keeps a keyed hash of the address each batch came from, never the address.
- **Vents you send.** A vent record contains the whole vent, with its text. A vent's text can mention things from your work, such as a project name. A vent leaves only when you press `x` on it, unless you turn on `analytics.share_vents`.

## How you use it

Each setting is off until you turn it on. On its first day the Chief offers to share usage analytics and vents with Attn's maintainers, and nothing turns on unless you say yes.

```sh
attn settings set analytics.enabled true
attn settings set analytics.sharing_enabled true
```

After Attn has sent something, **Settings → Analytics** offers **Forget this install** when you turn sharing off. **Forget this install** deletes what the server holds, then starts a new install. **Clear the local record** deletes the local outbox and dismissed vents.
