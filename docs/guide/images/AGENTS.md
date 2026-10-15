# Guide images

Screenshots and clips in the guide come from the demo harness. It runs the
real app and the real Claude Code and Codex binaries in a throwaway instance.
A local stub plays scripted model replies, so the frames show real agent
terminals with no account, no real repo and no model calls.

## Record

Build the demo instance once per checkout change. `DEMO=1` hides the instance
chip and titles the window "attn":

    make install INSTANCE=demo DEMO=1

Run a scene:

    node app/scripts/demo-harness/demo.mjs <scene> [--out <dir>]

Without `--out`, files land in `$TMPDIR/attn-demo/<scene>`. Copy the ones you
want into this folder. Each run starts from an empty instance, creates its
repos in `~/projects/attn-demo`, and removes them at the end. It refuses to
start if that folder already exists. When you're done recording,
`attn instance clean demo` removes the instance.

Scenes:

- `guide-stills` writes `home-screen.png`, `ledger-button.png` and
  `ledger-worktrees.png`.
- `agent-turn` records `agent-turn.mp4`, where a Claude session reads a file,
  edits it and asks a question.

Clips need the window recorder (`make install-window-recorder`) and screen
recording permission for it.

## Clips are raw footage

Clips get titles, zooms and captions in a later editing pass. The harness
records the full window at a steady 30 fps with nothing cropped or drawn on,
and writes `<clip>.events.jsonl` next to each clip. Every line is one scripted
action with `t`, its time in seconds from the start of the recording, so an
edit can line a zoom or a caption up with the moment something happens.

## Write a scene

A scene is a module in `app/scripts/demo-harness/scenes/` that names its repos
and drives the app:

```js
export default {
  repos: { recipes: { worktrees: ['search-by-ingredient'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt('Add a search by ingredient', turn()
      .say("I'll start with how recipes are searched today.")
      .read('src/search.js')
      .edit('src/search.js', { from: '...', to: '...' })
      .ask('Should it match partial names too?'));
    await demo.still('search.png');
  },
};
```

- Repos are copied from `app/scripts/demo-harness/fixtures/repos/<name>`.
  Worktrees sit next to them as `<repo>--<branch>`.
- `demo.session` opens a session and waits for the agent's prompt. The agent is
  `claude` unless you pass `agent: 'codex'`.
- `prompt` types like a person, and the stub plays the turn. The same turn
  works for both agents: `read`, `edit` and `run` become each agent's own
  tools. A turn ends with `ask` (the session waits for you), `done` (it goes
  idle) or `hold` after a `read`, `edit` or `run` (it keeps working until
  the scene ends). `prompt` returns
  once the session reaches that state.
- `demo.ui(action, payload)` calls the app's automation bridge
  (`app/src/hooks/useUiAutomationBridge.ts`).
- `demo.still(name, { crop, highlight })` captures the window. `crop` takes a
  selector, `highlight` draws a box around one.
- `demo.clip(name, async () => { ... })` records whatever the callback does.

A prompt with no scripted turn gets a reply that says so, in the terminal
where you'll see it.

Check every frame before publishing: a still should show nothing from your
machine beyond `~/projects/attn-demo`.
