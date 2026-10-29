# Callouts

An agent points at part of the app window with a short text, like a guided
tour. When the user asks where something is, or an agent wants the user to
look at another agent or a desktop, the agent shows a callout: a card beside
the named part of the window, one step at a time. The user steps through it
with Enter or →, goes to a named agent or desktop with Enter, and ends it with
Esc. Callouts are experimental and off until the user turns them on.

## Actions and ways in

- **Enable callouts.** Settings, Experimental, "Agent callouts" (`app/src/components/SettingsModalSections.tsx`) controls the home setting `callouts_enabled`; a person can run `attn settings set callouts_enabled true`. Every call checks the setting.
- **Point.** Agents run `attn callout anchors [--json]` and `attn callout show <anchor> <text> [<anchor> <text>]... [--session <id>]` (`cmd/attn/callout.go`); a person can use `--session <id>` to point from a session. A callout has up to 20 steps, each with an anchor and at most 160 characters; a new one replaces the current one. Anchors are `sidebar`, `home`, `commands`, `garden`, `desktops`, `queue`, `crew`, `settings-button`, `agent:<who>` and `desktop:<which>`. The anchors command reports which are on screen. The skill's reference describes when to point, anchors and limits (`internal/prompts/content/skills/attn/references/callouts.md`, named in `internal/prompts/content/skills/attn/SKILL.md`).
- **Follow or end.** The card names the agent, step count and text, and the ring marks its target (`app/src/callouts/CalloutOverlay.tsx`). Enter or → advances; Enter on an agent or desktop goes there. The card says "↵ Next", "↵ Done" or "↵ Go there", "→ Next" on agent and desktop steps, and "End callout". Escape or clicking outside ends it. An off-screen target says "<anchor> isn't on screen".

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`), in desktop flow.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-callouts-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-callouts.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

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

- The agent and a shell on the current desktop: `wsctl add-session --cwd $A --agent claude --label co-agent --id co-agent --place` and `wsctl add-session --cwd $A --agent shell --label co-shell --id co-shell --place`. `<agent>` is the agent's `runtime_id` in `wsctl list`.

- **Off by default.** Run `wsctl input --id <agent> --text "point-garden" --enter`. `./attn agent peek co-agent` shows the command failing with `callout show: callouts are off: the user can turn them on in Settings > Experimental > Agent callouts`.
- **The person turns them on.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`), click `[data-testid="settings-nav-experimental"]` natively, and click `[aria-label="Agent callouts"]` natively. `./attn settings get callouts_enabled` prints `true`. Close settings with Escape. From the person's shell, `./attn settings set callouts_enabled true` does the same.
- **List anchors.** Run `wsctl input --id <agent> --text "list-anchors" --enter`. Peek lists each anchor with `on screen` or `not on screen` and its description; in desktop flow `sidebar`, `commands` and `desktops` are on screen and `queue` is not.
- **Show a tour.** Press ⌘2 (`bridge native_key '{"key":"2","modifiers":["command"]}'`) first, so `arrangement.currentDesktopId` in `bridge get_state` is not desktop 1 and the last step's jump shows. Run `wsctl input --id <agent> --text "point-tour" --enter`. Peek shows `showing a callout with 3 steps; the user steps through it`. `bridge dom_wait '{"selector":".callout-card","textIncludes":"Your agents live here","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":".callout-heading"}'` reads `co-agent` and `1 of 3`.
- **Step through it.** Run `bridge native_key '{"key":"Enter"}'`: the card reads `Everything attn can do`. Run `bridge native_key '{"key":"ArrowRight"}'`: it reads `Your first desktop`, with "↵ Go there". Run `bridge native_key '{"key":"Enter"}'`: the card is gone (`bridge dom_wait '{"selector":".callout-card","absent":true,"timeoutMs":5000}'`), and `arrangement.currentDesktopId` in `bridge get_state` is desktop 1 again.
- **Point at an agent.** Run `wsctl input --id <agent> --text "point-agent" --enter`: `bridge dom_wait '{"selector":".callout-ring","timeoutMs":5000}'` matches over the shell's row. Run `bridge native_key '{"key":"Enter"}'`: the callout ends and `bridge get_state` shows `activeLeaf.sessionId` on `co-shell`.
- **End it.** Show the garden callout again with `point-garden`, then run `bridge native_key '{"key":"Escape"}'`: `.callout-card` is absent. Show it again and open the palette with ⌘⇧K: the callout ends as the palette opens. Show it again and click outside the card natively: it ends.
- **A missing anchor refuses the whole callout.** Run `wsctl input --id <agent> --text "point-queue" --enter`. Peek shows the command failing with `not on screen: queue; on screen now:` and the anchors that are. No card appears.
- **From the person's shell.** Run `./attn callout show --session co-agent garden "Pointed from the shell"`. It prints `showing a callout with 1 step; the user steps through it` and exits 0, and the card names `co-agent`. `./attn callout show garden` exits 1 with `callout show: an anchor needs its text: "garden" is the last argument`.
- **No app.** Quit the app with `./attn instance stop-app`, then run `./attn callout anchors --session co-agent`. It lists the anchors and exits 1 with `callout anchors: the attn app is not showing your profile`; `./attn callout show --session co-agent garden "x"` exits 1 with the same reason. Launch the app again with `bridge --launch --wait-ready ping`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek co-agent` after each agent call, and `bridge dom_text '{"selector":".callout-card"}'` at each step. Capture `bridge capture_screenshot '{"path":"<P>/callouts-tour.png"}'` on the first step and `<P>/callouts-agent.png` with the agent ring, `<P>` written out. Put the way in in each file's name, such as `callouts-agent-tour-peek.txt`.

## Contexts

- **Durability across restarts**: the setting is kept; a callout is not. A showing callout ends when the app relaunches, and the agent gets no word of it.
- **Linux**: `attn callout` works the same where the daemon and CLI run on Linux; it needs an app showing the profile, wherever that app runs.
- **Profiles**: a callout reaches only an app showing the caller's profile. `agent:` and `desktop:` resolve only in that profile, so another profile's agent is refused as `is not an agent in your profile`. A profile switch ends the callout.
- **Performance and idle cost**: an open card runs no animations once placed (`callouts` checks it). A browser tile hides while a callout shows and returns when it ends.
- **Harnesses**: every harness points with the same `attn callout` verbs and reads the same reference.
- **Keyboard and native input**: Enter, → and Esc drive the card, and any shortcut that opens another overlay ends it. Focus goes back where it was when the card ends, so typing reaches the terminal again.
- **Flow**: which fixed anchors are on screen follows the sidebar's shape. `queue` is there only in queue flow; with the queue bar or the collapsed sidebar, `sidebar` and `desktops` point at the strip, and an agent step reveals the agent list when it can.

## Tests

- Wire: `internal/daemon/callouts_wire_test.go`.
- Stack: `cmd/attn/callout_stack_test.go`.
- Scenario: `callouts`.
- App: `app/src/App.callouts.test.tsx`.

## Gotchas

- The setting is off on every fresh instance. A refusal naming Settings > Experimental > Agent callouts means the switch, not a bug.
- While another overlay is open (Settings, the palette, What's new, the notebook), `callout show` is refused with `not on screen:` naming every anchor of the callout and `on screen now:` empty. Close the overlay and call again.
- `callout show` returns as soon as the app accepts the card, before the user reads it; there is no completion. Prove the stepping from the app with `bridge`, not from the CLI.
- An agent anchor whose agent no tile shows still shows its step, saying it isn't on screen, and Enter still goes there.
- In queue flow `queue` is on screen and `desktops` may not be; list anchors before choosing them in a recipe.
- `bridge dom_click` on the card's buttons skips native input; drive the steps with `native_key`.
- A step over 160 characters or a 21st step refuses the whole callout, naming the limit and the count; the callout already showing stays.
- Working but wrong: Enter on an agent or desktop step goes there and ends the callout, so the steps after it never show; → moves on without going. A tour that puts such a step in the middle loses its tail when the user presses Enter.
