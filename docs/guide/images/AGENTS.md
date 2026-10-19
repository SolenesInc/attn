# Guide images

Screenshots and clips in the guide come from the demo harness. It runs the
real app and the real Claude Code and Codex binaries in a throwaway instance.
A local stub plays scripted model replies, so the frames show real agent
terminals with no account, no real repo and no model calls.

## Record

Build the demo instance once per checkout change. `DEMO=1` hides the instance
chip and the instance banner in terminals, and titles the window "attn":

    make install INSTANCE=demo DEMO=1

Run a scene:

    node app/scripts/demo-harness/demo.mjs <scene> [--out <dir>]

Without `--out`, files land in `$TMPDIR/attn-demo/<scene>`. Copy the stills
you want into this folder; clips go through the polish step below, which
writes them here. Each run starts from an empty instance, creates its repos
and the profile's Notebook in `~/projects/attn-demo`, and removes them at the
end. It refuses to start if that folder already exists.
When you're done recording, `attn instance clean demo` removes the instance.

Scenes:

- `guide-stills` writes `home-screen.png`, `ledger-button.png` and
  `ledger-worktrees.png`.
- `agent-turn` records `agent-turn.mp4`, where a Claude session reads a file,
  edits it and asks a question.
- `new-agent`, `split-terminals`, `move-tiles` and `queue-on` record the clips
  the guide shows for starting an agent, splitting a shell, moving a tile
  between desktops and turning on the queue.
- `queue-loop` records the README's queue flow clip, and `chief-setup` the
  Chief's first-run setup and first session in getting-started.

Clips need the window recorder (`make install-window-recorder`) and screen
recording permission for it. The demo window floats above other windows while
a scene runs, and each clip puts it back on the main display (the one with the
menu bar). That display has to be a Retina one: on a display with scale 1 the
footage would come out blurry, so a clip refuses to record there.

## Polish a clip

The harness records the full window at a steady 30 fps with nothing drawn on,
and writes `<clip>.events.jsonl` next to each clip: one line per scripted
action, with `t` in seconds from the first recorded frame. The polish step
reads both and renders the clip the guide shows:

    node app/scripts/demo-harness/polish.mjs <dir>/<clip>.mp4 --out docs/guide/images/<clip>.avif

It fills the HyperFrames composition in
`app/scripts/demo-harness/polish/index.html` with the clip and its events,
renders it locally with the pinned `hyperframes` CLI (telemetry off, nothing
uploaded), and encodes a looping animated AVIF. The composition crops the
titlebar, writes each caption in a bar under the window, shows a keycap for
each key press, zooms onto what a scene focuses, draws a pointer for each
drag, and crossfades back to the first frame so the loop has no jump.
Stretches a scene marks with `demo.speed` play faster, with the rate in a
corner. It needs
Node 22 or newer and an `ffmpeg` built with `libsvtav1`. `--keep` leaves the
HyperFrames project and its MP4 in place, for `npx hyperframes preview`.

To change a clip after the UI changes, run its scene again and polish the new
recording. To change the look of every clip, edit the composition.

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
    await demo.clip('split', async () => {
      demo.caption('`Cmd+D` (`Ctrl+Shift+D` on Linux) splits a shell beside the agent');
      await demo.press('terminal.splitVertical');
      await demo.type('npm test');
      await demo.key('return');
      await demo.pause(2500);
    });
  },
};
```

- Repos are copied from `app/scripts/demo-harness/fixtures/repos/<name>`.
  Worktrees sit next to them as `<repo>--<branch>`. `recipeTurns.mjs` has the
  turns the scenes share.
- `demo.session` opens a session and waits for the agent's prompt. The agent is
  `claude` unless you pass `agent: 'codex'`.
- `prompt` types like a person, and the stub plays the turn. The same turn
  works for both agents: `read`, `edit` and `run` become each agent's own
  tools. A turn ends with `ask` (the session waits for you), `done` (it goes
  idle) or `hold` after a `read`, `edit` or `run` (it keeps working until
  the scene ends). `prompt` returns once the session reaches that state.
- `demo.press(shortcutId)` presses a shortcut's current binding and `demo.key`
  presses one key; both show as a keycap in the polished clip. `demo.type`
  types into whatever has focus.
- Shells a scene splits off run your login shell. fish and zsh get a plain
  prompt with no history or suggestions; any other shell loads your own
  config, prompt included.
- `demo.caption(text)` sets the caption from that moment on. Backticks mark
  keys.
- `demo.focus(selector)` zooms onto an element; `demo.focus(null)` zooms back
  out.
- `demo.drag(from, to)` drags with the mouse from one element's center to
  another's, and the polished clip draws the pointer.
- `demo.speed(rate)` plays the clip from that moment at `rate` times speed;
  `demo.speed(1)` goes back to real time. Use it for waits the viewer
  doesn't need to sit through.
- `demo.reply(prompt, turn, { cwd })` scripts the reply to a prompt that
  attn sends on its own, like the Chief's wake prompt, and returns a promise
  that settles when the agent sends it.
- Every run starts with attn holding back GitHub requests for a minute (the
  harness's own daemon connection fetches every PR again), and Home shows a
  rate-limit banner until then. Scenes that show Home call
  `demo.waitOutGitHubThrottle()` first.
- `demo.ui(action, payload)` calls the app's automation bridge
  (`app/src/hooks/useUiAutomationBridge.ts`).
- `demo.still(name, { crop, highlight })` captures the window without its
  titlebar.
  `crop: { selector, extendBottom }` crops to an element, with `extendBottom`
  adding that many pixels below it. `highlight` takes a selector and draws a
  box around it.
- `demo.clip(name, async () => { ... })` records whatever the callback does.

A prompt with no scripted turn gets a reply that says so, in the terminal
where you'll see it.

## Put it on a page

Pages show stills and clips the same way: `![alt](../images/<file>)`.
GitHub strips `<video>` from markdown files, so clips are animated images.
`attn help` prints the alt text in place of the image, so write alt text that
reads on its own as a sentence and names each shortcut with its Linux key.

Check every frame before publishing: a still or clip should show nothing from
your machine beyond `~/projects/attn-demo`.
