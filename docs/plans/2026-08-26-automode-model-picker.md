# Auto mode models are set in the app, from what pi can reach

## Why

The auto mode settings pane renders the model list read-only and tells the
reader to go use `attn automode model`. That is the only list in the pane with
no way in: environment slots are a direct write (`automode_env_slot`), and the
allow and hard-deny lists are direct writes too (`automode_pattern_add`,
`automode_pattern_remove`). Those pattern commands are already app-only — an
agent can reach `automode_propose` and nothing else — so the fence that stops
an agent approving its own change is drawn at app versus agent, not at direct
write versus proposal. Models being proposal-only protects nothing; the pane
simply never got a write.

Naming a model by hand is also the one step where a person has to know a string
attn never shows them. pi already knows which providers are logged in and what
models each carries.

## What changes

**The pane writes the model list.** A new app-only `automode_model_set` carries
the ordered list, and the store writes it the way promotion does today.
Promotion keeps working; the CLI keeps proposing. An agent still cannot write.

**The picker asks pi.** attn-pi answers a new `automode.models` RPC with the
providers it can reach and the models under each. The daemon calls it through
the plugin connection it already holds and passes the answer to the pane.

pi's storage stays pi's business: the plugin reads it, and the plugin already
resolves that directory (`PI_CODING_AGENT_DIR`, else `~/.pi/agent`) and owns
files inside it. The daemon learns nothing about pi's layout.

## The picker stays open

`ParseModelList` accepts any `provider/id`; pi's own `--models` takes globs and
fuzzy patterns. A closed dropdown would make the pane stricter than the CLI it
replaces, so what pi can reach is offered beside a field that still takes
anything. `models-store.json` carries `checkedAt`, so a stale catalog says when
it was read instead of just looking short.

## When pi cannot answer

The plugin may be uninstalled, disconnected, or starting. The pane says which,
and the field still takes a typed model — the picker is a convenience over a
control that works without it.

## Surfaces

- Protocol: two commands and one result event; 272 → 273.
- Daemon: the command pair, plus the plugin call and its failure paths.
- Store: `SetAutoModeModels`, and a fact so the pane re-reads.
- Plugin: `automode.models`, reading pi's auth and catalog files.
- App: the model editor, the picker, and the empty and error states.
- CLI: unchanged. `attn automode model` still records a proposal.
