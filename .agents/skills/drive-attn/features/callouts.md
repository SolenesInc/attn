# Callouts

An agent points at part of the app window with a short text, like a guided
tour. When the user asks where something is, or an agent wants the user to
look at another agent or a desktop, the agent shows a callout: a card beside
the named part of the window, one step at a time. The user steps through the
callout with Enter or →, goes to the agent or desktop the last step names, and
ends the callout with Esc. Callouts are always on.

## Sub-features

- `callout-show`: up to 20 steps, each an anchor and at most 160 characters of text. A new callout replaces the one showing.
- `callout-anchors`: the fixed anchors (`sidebar`, `home`, `commands`, `garden`, `asks`, `desktops`, `queue`, `crew`, `settings-button`) with which are on screen now, and the `agent:<who>` and `desktop:<which>` anchors.
- `callout-stepping`: Enter and → do the same on every step: they go to the next step, and on the last step they go to its agent or desktop and end the callout. Esc ends it, and so does a click outside a one-step callout.

## How to get to it

### People

- The callout card (`native/crates/attn-app/src/callouts.rs`), `callout`, is a dialog named "Callout from <agent>". It shows the pointing agent's name and the step count ("<i> of <n>", `callout.count`), the text (`callout.text`), and "↵ Next", or on the last step "↵ Done", or "↵ Go there" when that step names an agent or desktop (`callout.next`). It also shows "End callout" (`callout.end`). A step whose anchor is not on screen says "<anchor> isn't on screen" (`callout.unanchored`).
- Where people see what agents do: the card names the agent that points, and the ring (`callout.ring`) marks what it points at.
- Their own shell: `attn callout show --session <id>` to point from a session.

### Agents

- `attn callout anchors [--json]` and `attn callout show <anchor> <text> [<anchor> <text>]... [--json] [--session <id>]`. The catalog defines both (`internal/catalog/content/callout.yaml`); the CLI presents them (`cli/attn/src/commands/callout/mod.rs`) and the command layer runs them (`internal/daemon/callouts.go`).
- Tools, with `agent_tools_enabled` on: `callout_anchors` and `callout_show` over `/mcp`. `callout_show` takes `steps`, a list of `{anchor, text}`.
- Prompts: `attn skill --reference callouts` covers when to point, the anchors and the limits. The catalog generates it (`internal/prompts/content/skills/attn/references/callouts.md`). The skill's capability index names the reference (`internal/prompts/content/skills/attn/SKILL.md`).

## Driving it

The card takes the keyboard when it shows. When it ends, nothing has focus, so keys go to the window and the terminal holding it. Tiles and the card draw in one window, so only a browser tile's page, a native view over the window, hides while a callout shows ([browser.md](browser.md)). The ring, `callout.ring`, is drawn but is not in the accessibility tree, so `bridge tree` does not list it. See it in a `bridge shot`.

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, and stay in desktop flow. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`, and use the Linux chords: `mod+alt+k` for the command palette and `mod+shift+2` for desktop 2.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-callouts-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-callouts.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "list-anchors", "actions": [
    {"type": "attn", "args": ["callout", "anchors"]}]},
  {"includes": "point-garden", "actions": [
    {"type": "attn", "args": ["callout", "show", "garden", "Work you and your agents track"]}]},
  {"includes": "point-tour", "actions": [
    {"type": "attn", "args": ["callout", "show", "sidebar", "Your agents live here", "commands", "Everything attn can do", "desktop:1", "Your first desktop"]}]},
  {"includes": "point-agent", "actions": [
    {"type": "attn", "args": ["callout", "show", "agent:co-shell", "This shell waits for you"]}]},
  {"includes": "point-queue", "actions": [
    {"type": "attn", "args": ["callout", "show", "queue", "Waiting turns come first"]}]}
]}
```

- Start the agent and a shell on the current desktop with `wsctl add-session --cwd $A --agent claude --label co-agent --id co-agent --place` and `wsctl add-session --cwd $A --agent shell --label co-shell --id co-shell --place`. `<agent>` is the agent's `runtime_id` in `wsctl list`.

- **Always on.** On a fresh instance, run `wsctl input --id <agent> --text "point-garden" --enter`. `./attn agent peek co-agent` shows the callout showing.
- **List anchors.** Run `wsctl input --id <agent> --text "list-anchors" --enter`. Peek lists each anchor with `on screen` or `not on screen` and its description. On home in desktop flow, `sidebar`, `home`, `commands`, `garden`, `asks`, `desktops` and `settings-button` are on screen, and `queue` and `crew` are not.
- **Show a tour and step through it.** Press `mod+2` (`desktop.select2`) with `bridge key '{"chord":"mod+2"}'` first, so the window is not on desktop 1. Run `wsctl input --id <agent> --text "point-tour" --enter`, then `bridge wait '{"target":{"key":"callout","name":"Callout from co-agent"}}'`. Peek shows `showing a callout with 3 steps; the user steps through it`, and `callout.count` reads "1 of 3". Press `Enter` with `bridge key '{"chord":"Enter"}'`: `callout.text` reads "Everything attn can do". Press `Right`: `callout.text` reads "Your first desktop" and `callout.next` is named "↵ Go there". Press `Enter`: `callout` goes and the window shows desktop 1.
- **Point at an agent.** Run `wsctl input --id <agent> --text "point-agent" --enter`: the card shows beside `sidebar.session.co-shell`. Press `Enter`: the callout ends and the shell's desktop shows.
- **End it.** Show `point-garden` and press `Escape`: `callout` goes. Show it and press `callout.end` (`bridge press '{"target":{"key":"callout.end"}}'`): `callout` goes. Show it and open the palette with `bridge run '{"name":"ui.commandPalette"}'`: the one-step callout ends as the palette opens. Show a one-step callout on `sidebar` and press `sidebar.home` beside it: the callout ends. The garden card sits over the top of the sidebar, so a press aimed at `sidebar.home` then lands on the card. Show `point-tour`, open the palette with the palette key and close it with `Escape`: the card comes back on the same step with the keyboard. Press `sidebar.home`: the card stays, and `Enter` goes to the window until you press `callout.text`.
- **A missing anchor refuses the whole callout.** Run `wsctl input --id <agent> --text "point-queue" --enter` in desktop flow. Peek shows the command failing with `not on screen: queue; on screen now:` and the anchors that are. No card shows.
- **From the person's shell.** Run `./attn callout show --session co-agent garden "Pointed from the shell"`. It prints `showing a callout with 1 step; the user steps through it` and exits 0, and `callout.from` reads `co-agent`. `./attn callout show garden` exits 1 with `callout show: each anchor needs its text: "garden" is the last argument` and the usage line.
- **No app.** Quit the app with `./attn instance stop-app`, then run `./attn callout anchors --session co-agent`. It lists the anchors and exits 1 with `callout anchors: the attn app is not showing your profile`. `./attn callout show --session co-agent garden "x"` exits 1 with the same reason. Launch the app again as [../SKILL.md](../SKILL.md) says.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek co-agent` after each agent call, and `bridge tree` with the card open. Capture `bridge shot '{"path":"<P>/callouts-tour.png"}'` on the first step and `<P>/callouts-agent.png` with the agent ring, `<P>` written out. Name each file for its way in, such as `callouts-agent-tour-peek.txt`.

### In the Tauri app (the rollback)

Install it as [../SKILL.md](../SKILL.md#the-tauri-app-the-rollback) says. Check that `bridge --launch --wait-ready ping` answers and run `bridge dismiss_whats_new`. The card is `.callout-card` (`app/src/callouts/CalloutOverlay.tsx`), with its heading in `.callout-heading` and its ring in `.callout-ring`. In desktop flow `sidebar`, `commands` and `desktops` are on screen and `queue` is not.

- **Show a tour.** Press ⌘2 (`bridge native_key '{"key":"2","modifiers":["command"]}'`) first, so `arrangement.currentDesktopId` in `bridge get_state` is not desktop 1 and the last step's jump shows. Run `wsctl input --id <agent> --text "point-tour" --enter`. `bridge dom_wait '{"selector":".callout-card","textIncludes":"Your agents live here","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":".callout-heading"}'` reads `co-agent` and `1 of 3`.
- **Step through it.** Run `bridge native_key '{"key":"Enter"}'`: the card reads `Everything attn can do`. Run `bridge native_key '{"key":"ArrowRight"}'`: the card reads `Your first desktop`, with "↵ Go there". Run `bridge native_key '{"key":"Enter"}'`: the card is gone (`bridge dom_wait '{"selector":".callout-card","absent":true,"timeoutMs":5000}'`), and `arrangement.currentDesktopId` in `bridge get_state` is desktop 1 again.
- **Point at an agent.** Run `wsctl input --id <agent> --text "point-agent" --enter`: `bridge dom_wait '{"selector":".callout-ring","timeoutMs":5000}'` matches over the shell's row. Run `bridge native_key '{"key":"Enter"}'`: the callout ends and `bridge get_state` shows `activeLeaf.sessionId` on `co-shell`.
- **End it.** Show the garden callout again with `point-garden`, then run `bridge native_key '{"key":"Escape"}'`: `.callout-card` is absent. Show it again, click `[aria-label="End callout"]` natively and check that the card is absent. Show it again and open the palette with ⇧⌘K: the callout ends as the palette opens. Show it again and click outside the card natively: the callout ends. These show one step. Show a callout with several steps and open the palette: the card hides, and comes back on the same step when the palette closes. Click outside it natively: the card stays, and Enter, → and Esc go to what you clicked until you click the card.
- **Proof.** Keep `bridge dom_text '{"selector":".callout-card"}'` at each step. Capture `bridge capture_native_window_screenshot '{"path":"<P>/callouts-tauri-tour.png"}'` on the first step, `<P>` written out. `bridge capture_screenshot` draws the page without the card or its ring, and `bridge dom_click` on the card's buttons skips native input, so drive the steps with `native_key`.

## Contexts

- **Durability across restarts**: a showing callout ends when the app relaunches, and attn does not tell the agent.
- **Linux**: `attn callout` works the same where the daemon and CLI run on Linux. It needs an app showing the profile, wherever that app runs.
- **Profiles**: a callout reaches only an app showing the caller's profile. `agent:` and `desktop:` resolve only in that profile, so another profile's agent is refused as `is not an agent in your profile`. A profile switch ends the callout.
- **Performance and idle cost**: an open card runs no animations once placed. The Tauri app's `callouts` scenario checks it. A browser tile's page hides while a callout shows and returns when the callout ends. In the Tauri app (the rollback), the whole browser tile hides.
- **Harnesses**: every harness points with the same `attn callout` verbs and reads the same reference.
- **Keyboard and native input**: Enter, → and Esc drive the card, and any shortcut that opens another overlay ends a one-step callout and hides a longer one until the overlay closes. When the card ends, the app leaves nothing focused, so typing reaches the terminal that holds the keyboard. In the Tauri app (the rollback), the terminal gets focus back.
- **Flow**: which fixed anchors are on screen follows the sidebar's shape. `queue` is there only in queue flow. With the queue bar or the collapsed sidebar, `sidebar` and `desktops` point at the strip, and an agent step reveals the agent list when it can.

## Tests

- Wire: `native/crates/attn-app/tests/wire/callouts.rs`, `internal/daemon/callouts_wire_test.go`, `internal/daemon/agent_tools_callout_wire_test.go` (the tools).
- Stack: `cmd/attnd/callout_stack_test.go`.
- Scenario: `native-callouts`, and in the Tauri app (the rollback) `callouts`.
- App: the Tauri app's (the rollback), `app/src/App.callouts.test.tsx`.

## Gotchas

- While another overlay is open (Settings, the palette, What's new, the notebook), `callout show` is refused with `not on screen:` naming every anchor of the callout and `on screen now:` empty. Close the overlay and call again.
- `callout show` returns as soon as the app accepts the card, before the user reads the card. There is no completion. Prove the stepping from the app with `bridge`, not from the CLI.
- An agent anchor whose agent no tile shows still gets its step. The step says the agent isn't on screen, and Enter on the last step still goes to the agent.
- In queue flow `queue` is on screen and `desktops` may not be. List anchors before choosing them in a recipe.
- `crew` is never on screen: the app does not anchor it to the sidebar's crew row yet, so a callout that names it is refused.
- If a callout has a step over 160 characters or a 21st step, attn refuses the whole callout, naming the limit and the count. The callout already showing stays.
