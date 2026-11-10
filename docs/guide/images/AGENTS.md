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
repos in `~/projects/attn-demo`, and removes them at the end. The run refuses to start if that folder already exists. When you're done recording,
`attn instance clean demo` removes the instance.

To record on another instance, such as when `demo`'s port is taken, build it
with `make install INSTANCE=<name> DEMO=1`, set `ATTN_DEMO_INSTANCE=<name>` for
`demo.mjs`, and clean that instance at the end.

Scenes:

- `guide-stills` writes `home-screen.png`, `ledger-button.png` and
  `ledger-worktrees.png`.
- `agent-turn` records `agent-turn.mp4`, where a Claude session reads a file,
  edits it and asks a question.
- `queue-flow` records `queue-flow.mp4`, the raw footage of the
  [queue flow video](#queue-flow-video).

Clips need the window recorder (`make install-window-recorder`) and screen
recording permission for it.

## Clips are raw footage

Clips get titles, zooms and captions in a later editing pass. The harness
records the full window at a steady 30 fps with nothing cropped or drawn on,
and writes `<clip>.events.jsonl` next to each clip. Every line is one scripted
action with `t`, its time in seconds from the start of the recording, so an
editor can place a zoom or a caption at the moment something happens.

## Queue flow video

The website's queue flow page plays `website/public/recordings/queue-loop.mp4`.
The video is cut from one raw clip with HyperFrames, and the project is
`app/scripts/demo-harness/queue-flow-video/`. To make it again after the UI
changes, record the scene and build:

    node app/scripts/demo-harness/demo.mjs queue-flow --out <dir>
    node app/scripts/demo-harness/queue-flow-video/build.mjs --clip <dir>/queue-flow.mp4

`build.mjs` places every cut, zoom, caption and sound from the clip's events
and the narration, renders on this machine, and writes the video and its poster
(`.jpg`). `--draft` renders faster for a look, and `--out <file>` writes
somewhere else. HyperFrames telemetry is off, and nothing is uploaded.

- The build stops when the clip shows no change where its events record one.
  The window recorder drops frames on a loaded Mac; record the scene again.
- `narration.json` holds the script, one line per `id`. After you change a
  line, speak it again with
  `ELEVENLABS_API_KEY=... node app/scripts/demo-harness/queue-flow-video/narrate.mjs --only <id>`,
  which writes `audio/voice/<id>.mp3` and the word timings in
  `audio/voice/<id>.words.json`. Without `--only` it speaks every line, and
  `--voice <voice id>` picks another ElevenLabs voice. `audio/CREDITS.md` says
  where the voice, the music and the sound effects come from.
- Zooms and outlines follow the scene's `mark` events, and the zoom factors are
  constants at the top of `build.mjs`. If the scene's prompts or replies
  change, check those frames.
- `composition/template.html` holds the look: layout, type, colors and motion.
- The page shows the narration as a transcript read from `narration.json`.
  Don't give the video a captions `<track>`: with one, the video never loads
  in Safari on some iPhones.

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
  tools. A turn ends in one of three ways: `ask` leaves the session waiting for you, `done` leaves it idle, and `hold` after a `read`, `edit` or `run` leaves it working until the scene ends. `prompt` returns when the session reaches the state that the turn ends in.
- `demo.ui(action, payload)` sends an action to the instance's UI automation
  endpoint on localhost, which lets a script click, type and read the running
  app (`app/src/hooks/useUiAutomationBridge.ts`).
- `demo.still(name, { crop, highlight })` captures the window. `crop` takes a
  selector, `highlight` draws a box around one.
- `demo.clip(name, async () => { ... })` records whatever the callback does.
- `demo.mark(name, selector)` and `session.markText(name, text)` add a `mark`
  event with the place of an element, or of the last screen line that shows
  the text, so an editor can point at it.
- In a turn, `after(promise)` holds the next step until the promise resolves,
  for an agent that must stay busy until a moment the scene chooses. `prompt`
  then returns as soon as the session is working.

A prompt with no scripted turn gets a reply that says so, in the terminal
where you'll see it.

Check every frame before publishing: a still should show nothing from your
machine beyond `~/projects/attn-demo`.
