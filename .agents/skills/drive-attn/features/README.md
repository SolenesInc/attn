# attn feature map

This map is the maintained source for what each attn feature is, how people
and agents reach it, how to drive it on a non-production instance, and where
its committed tests live. Read it before checking a change live: find the
feature's file in the index, then drive every way in the file lists. A proof
through one convenient way in is incomplete while the file lists others.

## Audiences

- **People**: the user, through the app and their own shell.
- **Agents**: the agents attn runs. They use the `attn` CLI and read the
  prompts attn sends them.
- **Maintainers**: the agents and people changing attn itself. This map is
  written for them.

In the index, "both" means people and agents.

## Baseline preconditions

- A fresh named instance, launched as [../SKILL.md](../SKILL.md) describes:
  built from this checkout, the mock agent for `claude` and `codex`, headless
  model tasks off, the mock GitHub, a scratch tool home (`ATTN_TOOL_HOME`)
  and an analytics upload address that goes nowhere
  (`ATTN_ANALYTICS_INGEST_URL`). The doctor passes before the first action.
- On a fresh app, What's new covers the home screen and holds the palette
  and most shortcuts. Run `bridge dismiss_whats_new` first.
- A feature that needs realistic data says so in its own preconditions, and
  copies production data out read-only. Never point an instance at
  `~/.attn`.
- A feature that needs more in the daemon's environment says so in its
  preconditions: export it in the instance shell, then stop the daemon and
  start it again as [../SKILL.md](../SKILL.md) describes.
- The baseline has no stand-in for Pi. A step that needs Pi, its plugin or
  the Guardian fails on the baseline, and its file names the scenarios that
  run the real `pi`.
- Never drive an instance this run did not start.

## Driving conventions

### Doors and harnesses

- People: `bridge` with native input (`native_key`, `native_text`,
  `native_mouse`) for shortcuts, the palette and controls; `./attn` for the
  person's own shell.
- Agents: a mock agent whose fixture runs the verb with an `attn` action.
  `./attn` from the instance shell stands in for an agent only on a verb
  that needs no session. A verb whose answer depends on the caller (close,
  reload, snooze, `attn terminal`, `attn turn`) behaves differently from a
  plain shell, so drive the agent's side through the mock.
- The person's own shell can act as a session with `--session <id>` or
  `--source-session <id>`. That is a door of its own, and a way to set up,
  never proof of what an agent can do.
- Triggers: cause the real event (an agent stops, a note rings, a pull
  request changes on the mock GitHub) and observe what follows. Where the
  mock cannot raise a harness's event, the step may call the hidden hook
  verb the harness would (`./attn _hook-<event>`) and says so.
- Protocol reads, sessions and terminal input: `wsctl`.
- Plain shell tools set up and read: files, `git`, `jq`, `curl` against the
  mock GitHub, `file`, `stat`, `pbpaste`, `python3 -m http.server` for a
  local page, and the repository's own scripts a file names. A process a
  recipe starts in the background keeps its PID from `$!` and stops by that
  PID.
- A verb that calls a harness CLI itself, outside the daemon, never reaches
  the mock agent. Its recipe puts a stand-in on `PATH`, as
  [vision-check.md](vision-check.md) does.
- `--help` is not universal. `attn list` ignores its arguments and prints
  every session, and `attn plugin` and `attn automation` take `--help` as an
  unknown subcommand; read their source instead of asking them.
- `./attn` is this checkout's build, run in the instance shell. `wsctl` and
  `bridge` are the shell functions from [../SKILL.md](../SKILL.md). Every
  command is literal: keep names, flags and JSON as written.

### Sessions and the mock agent

- `wsctl add-session --cwd <dir> --agent <harness> --label <label> --id
  <session>` starts a session on the profile's current desktop beside the
  active tile, with or without `--place`.
- `<terminal>` is a session's terminal id: the `runtime_id` of the entry in
  `wsctl list`'s `desktops[].panes[]` whose `session_id` is the session.
  `wsctl input --id <terminal>` takes it, though wsctl's help calls it a
  session id. Read it again after a reload, a resume or a crew wake, which
  can start a new terminal.
- A shell's id, which `bridge` actions take as `sessionId`, is the `id` of
  its entry with `"agent": "shell"` in `wsctl list`; files call it
  `<shell-id>`.
- The mock reads `.attn-mock-agent.json` from its folder at launch. A
  fixture is literal JSON: the mock expands no environment variables, so a
  path in it is written out (`<A> written out`). Turns are tried in order,
  and the first whose `includes` matches runs.
- A mock turn stays `working` for at least 1.5 seconds, so a reply on screen
  does not mean the turn ended. Before the next prompt, re-read
  `./attn agent peek <session>` until its `state:` line stops reading
  `working`.
- The mock prints at the terminal's width, 80 columns by default, so a long
  line wraps anywhere. Match a line's first words. A refused `attn` action
  shows as a `mock agent error:` block.
- Text typed through `wsctl input` counts as the user typing: attn types no
  ring, answer or quick capture into that terminal for 30 seconds after it.

### In the app

- Prefer stable handles to coordinates and tab order: shortcut ids, palette
  command ids, `aria-label` and `data-testid` values, session and seed ids.
- Shortcuts: press the macOS binding with `bridge native_key`. The binding
  for an id is in `app/src/shortcuts/registry.ts`, or ask the app:
  `bridge shortcut_binding '{"shortcutId":"<id>"}'`.
- The palette: press ⌘⇧K (`ui.commandPalette`), wait for
  `[role="combobox"][aria-label="Commands"]` to take focus, type the
  command's title with `bridge native_text`, then press Enter. The map names
  the command id; you type its title.
- Settings: press ⌘, (`ui.openSettings`), then click the section's nav
  button, `[data-testid="settings-nav-<section>"]`, natively.
  [settings.md](settings.md) lists the section ids.
- A native click: `bridge native_mouse` takes `x` and `y` from 0 to 1 across
  the window. Take the element's box from `bridge dom_bounds
  '{"selector":"..."}'` (`bounds`), the window's from `bridge
  get_window_bounds` (`logicalBounds`), and the web view's from `bridge
  dom_focus_state` (`viewport`). The web view sits below the title bar and
  centered across the window, so
  `x = (max(0, window.width - viewport.innerWidth) / 2 + box.x + box.width / 2) / window.width`
  and
  `y = (max(0, window.height - viewport.innerHeight) + box.y + box.height / 2) / window.height`.
- `native_mouse` actions are `click`, `right_click`, `move` and `drag` (with
  `toX` and `toY`), each with optional `modifiers`. A control that takes
  clicks only while the pointer is over its row, such as a sidebar row's
  buttons, needs a `move` over it first.
- `native_key`, `native_text` and `native_mouse` act on the main window;
  `"window":"capture"` in their JSON sends them to quick capture's panel.
- A drawer (attention, asks, automations, garden) stays in the page while
  closed, hidden with `aria-hidden="true"`. Tell open from closed by that
  attribute, not by whether an inner selector is present.
- DOM actions (`dom_click`, `dispatch_shortcut`, `settings_select_section`,
  `sessions_set_filter` and the like) skip native input: use them to set up,
  never as proof of a person's way in.

### Waiting

- Wait on real signals, never on sleeps. In the app, `bridge dom_wait` takes
  `selector` and `timeoutMs`, and optionally `textIncludes`, `absent: true`
  and `focused: true`.
- A read right after a native key, a click or a command can run before the
  daemon answers. Where a step names a state, re-read the command or
  `bridge` read that shows it until the state appears, with a deadline that
  fails the run: 30 seconds unless the step names another.
- For an agent, re-read `./attn agent list` or `./attn agent peek <session>`
  until the state or reply you expect appears, with a deadline that fails
  the run.

### Runs

- Start each recipe from the baseline unless its preconditions say
  otherwise. Remove what a recipe created when the next one needs a clean
  slate, but never the proof.
- Each recipe keeps its proof in `P=$(mktemp -d
  /tmp/drive-attn-<feature>.XXXX)`, made in its preconditions. `bridge` JSON
  is single-quoted, so a path in it is written out: `<P>` is that folder's
  path.
- A way in is driven once, in the file that owns it; other files link there.
  A way in the harnesses cannot reach keeps its step, which says so and
  fails; the reason goes in that file's Gotchas.

## Proof and skip reporting

- Capture the action and the resulting state, not only the final screen.
- App proof is a `bridge` read (`get_state`, the feature's `*_get_state`, or
  `dom_text`) and a `bridge capture_screenshot` that shows the instance
  marker in the sidebar: `instance <name>` while the sidebar is expanded,
  `<name>` on the collapsed sidebar or the queue bar
  ([instances.md](instances.md)).
- CLI proof is the command, stdout, stderr and exit code.
- A change proves itself from a second view: read a change made in the app
  back through `./attn ... --json`, and a change made from the CLI back in
  the app.
- Record the feature id and the way in with every artifact.
- Report a way in you could not reach with the command you tried and the
  precondition that failed. Never report a skipped way in as verified
  through another.
- When a precondition the baseline lacks blocks a recipe, the recipe fails
  and the reason goes in that feature's Gotchas. Never weaken the recipe to
  make it pass.

## Feature entry contract

Every feature file has these sections, in this order:

1. `# <Name>`, then one short paragraph from the user's point of view: what
   the feature is and what it is for. It may end by naming the files that own
   related behavior.
2. `## Sub-features`: short ids in backticks, one line each.
3. `## How to get to it`, with `### People` and `### Agents` always present.
   Add `### Triggers` only when something starts the feature without anyone
   invoking it.
   - People: shortcuts as the macOS binding with the registry id, palette
     command ids, native menus, deep links, controls, where people see and
     steer what agents do, and "Their own shell:" for the verbs they run.
   - Agents: every `attn` verb and subcommand an agent can run for the
     feature, whatever audience the index names, and the prompts attn sends
     the agents it runs. People's "Their own shell:" points at these verbs
     instead of repeating them. A setting the feature only reads through
     `attn settings` may sit under People's own shell alone, with
     [settings.md](settings.md) owning the verb.
   - Write "None." when an audience has no way in, so the gap shows. One
     sentence may follow, saying where that audience sees the feature
     instead.
   - Each way in names its source file once.
4. `## Driving it`: a `Preconditions:` list, then labeled bullets that pair
   one action with an exact command and what you observe: "- **Action.** What
   the person or agent does. Run `<command>`. <What you see>." An action that
   needs setup, a native action and a read chains those commands in one
   bullet. Use only the harnesses and conventions above. Cover every way in
   the file owns, and end with a **Proof.** bullet naming what to capture.
5. `## Contexts`: only the contexts below that apply, one bullet each saying
   how the feature behaves under it. A context left out does not apply.
6. `## Tests`: pointers by kind, as paths or scenario ids, and nothing else.
   Leave out a kind with no tests.
   - Wire: a real daemon driven as a protocol client:
     `internal/daemon/*_wire_test.go`, and the other `internal/daemon` tests
     that drive a daemon.
   - Stack: real processes together: `*_stack_test.go` in any package and
     `app/e2e/*.spec.ts`.
   - Scenario: an id in `app/scripts/real-app-harness/scenarioCatalog.mjs`;
     a `scenario-*.mjs` script outside the catalog by its path, after
     "outside the catalog".
   - Kernel: package tests that start no daemon, such as a specified
     function's tables or properties.
   - App: frontend tests under `app/src`, the app wire tests `App.*.test.tsx`
     included.
   - A `scripts/verify-*` bundle sits under the kind of the tests it runs,
     and under Stack when it runs more than one kind.
7. `## Gotchas`: traps that waste or invalidate a run, and what "working but
   wrong" looks like for the feature.

Write from the user's point of view. Name user paths, stable handles,
required state, commands and observable results, never how the code works
inside; a source path appears only to anchor a way in, a test, or a prompt
whose text a gotcha quotes. Link
another feature as `[<id>.md](<id>.md)`. Use the terms in `docs/glossary.md`
and none of its legacy names in prose. Exact handles and output are
literal: a command, flag, id, selector, JSON key, CLI output or UI label is
written as the product has it, in backticks or quotes, even when it carries
an older name the glossary lists (`waiting_input`, `activeLeaf`, `panes`,
`runtime_id`, "Close Pane", "Reopen", "Wake"). The file's Gotchas name such a
label as working but wrong. Every command, shortcut id, palette id, verb,
path and scenario id must exist at HEAD.

## Contexts

- **Durability across restarts**: what the feature keeps across app, daemon
  and machine restarts, and what an agent on the embedded backend loses.
- **Linux**: how the feature works where the daemon and CLI run on Linux,
  and its Linux key bindings, which add Ctrl, Shift or Alt where macOS uses
  ⌘; each file writes its bindings out in full.
- **Outposts**: how the feature behaves when a terminal, delegation or
  notebook lives on an outpost, including while its host is offline.
- **Profiles**: what belongs to one profile, and what another profile cannot
  see.
- **Performance and idle cost**: memory and CPU while idle, repainting, and
  the limits a large garden, ledger or session meets.
- **Harnesses**: how the feature differs across Claude Code, Codex and Pi.
- **Keyboard and native input**: the keys that reach every action, where
  focus lands, and macOS menu accelerators that can take a key first.
- **Compatibility across versions**: a later version reading today's data
  and talking to agents an earlier version started, and retired commands.
- **Flow**: what changes between queue flow and desktop flow.

## Features

How the recipes were checked: [garden.md](garden.md)'s Driving it ran live
on a named instance, daemon and CLI only, at commit `b3d0df90a`: every step
from the instance shell and the Agents door, where the mock agent tended,
watched, armed a harvest on merge and harvested. Its `bridge` steps, and
every other file's Driving it, come from reading the source and the CLI's
help at that commit, and have not run yet. A file whose recipe runs live
says so here.

### Agents and sessions

- [Agent launch](agent-launch.md) (both): Start an agent in a terminal tile, reload it, and close it.
- [Agent state](agent-state.md) (both): What each agent is doing right now (working, waiting, needs approval…), like a presence status, and why attn thinks so.
- [Ledger](ledger.md) (both): Every session, live and closed, like a chat history: resume a closed one as itself, keep or forget its conversation.
- [Usage and cost](usage.md) (people): Tokens, cost and context window for each session.
- [Harnesses](harnesses.md) (people): Bring your own harness: attn wraps Claude Code, Codex and Pi as they are, and plugins add more.
- [Shared Codex](shared-codex.md) (people): Codex sessions share one app-server that attn keeps updated and restarts.
- [Auto mode](auto-mode.md) (both): Rules and a guardian model answer agents' permission prompts; agents propose rules, only the user promotes them.

### Attention

- [Queue flow](queue-flow.md) (both): attn brings the user the next agent that needs them and moves them on after every prompt, like working an inbox to zero.
- [Sidebar](sidebar.md) (people): The window's sidebar; its shape follows the profile's flow.
- [Home screen](home.md) (people): The dashboard of what asks for the user's attention across agents, with notifications.
- [Palette](palette.md) (people): Search and run commands, agents and tiles from the keyboard, like the VS Code command palette; it learns what the user picks.

### Desktops and tiles

- [Desktops and tiles](desktops.md) (both): Arrangements of tiles, like macOS Spaces: switch, split, sort, and move tiles between desktops.
- [Terminals](terminals.md) (both): A real PTY in a tile: what the user types and what the program prints, with scrollback, find, links and images.
- [Markdown tiles](markdown.md) (both): Open a markdown file in a tile, rendered and live as the file changes.
- [Notebook](notebook.md) (both): The user's notebook and daily journal, edited in a tile or fullscreen; the chief keeps it too.
- [In-app browser](browser.md) (both): A browser tile that the user and their agents open and drive.
- [Annotations](annotations.md) (both): Select text in a live terminal or a rendered markdown file, comment on it, and send the batch to a session as one message.
- [Callouts](callouts.md) (both): An agent points at part of the app window with a short text, like a guided tour.

### Coordination

- [Garden](garden.md) (both): The work tracker: seeds planted, tended and harvested; the unit agents hand off, work on, and stay organized with. Driving it ran live for the shell and the agents.
- [Delegation](delegation.md) (both): One agent starts another on a seed, from any harness to any harness, as a full session the user can open and steer.
- [Inbox](inbox.md) (agents): Messages, seed updates and quick captures addressed to an agent or a mailbox; a ring prompts the agent to read them, like a push notification.
- [Asks](asks.md) (both): An agent asks the user a question and keeps working; the user answers in their own time, and the answer lands in the agent's conversation.
- [Crew](crew.md) (both): Permanent agents with charters, who leave letters for their next session; the chief coordinates the profile.
- [Automations](automations.md) (both): attn starts a steerable agent on a schedule or an event.
- [Quick capture](quick-capture.md) (people): Send a message with attachments to the chief or crew from anywhere, with a global shortcut, like macOS Quick Note.

### Code

- [Pull requests](pull-requests.md) (both): PRs linked to the sessions that opened them, watched until they are ready.
- [Worktrees](worktrees.md) (both): The git worktrees agents work in: created for delegations, kept or swept when the work is done.

### Configuration

- [Settings](settings.md) (both): The settings dialog and attn settings: home settings shared with outposts, machine settings per machine.
- [Keyboard shortcuts](shortcuts.md) (people): Every action has a shortcut, listed in a cheat sheet and rebindable.
- [Profiles](profiles.md) (people): Separate worlds for the user's work; nothing in one profile exists in another.
- [Outposts](outposts.md) (both): Agents and terminals on other machines, driven from the home's app.

### Agent toolkit and operations

- [Agent guidance](agent-guidance.md) (agents): What attn tells the agents it runs: the attn skill, session instructions and prompts.
- [Document store](doc-store.md) (agents): Collections of documents with live queries, for agents and plugins.
- [Vents](vents.md) (agents): Agents report what got in their way, for the user to review later, like a bug report.
- [Vision check](vision-check.md) (agents): Ask a model one question about an image.
- [Telemetry](telemetry.md) (people): What attn records about its own use, kept in an outbox and uploaded only when the user turns it on.
- [Diagnostics and repair](diagnostics.md) (maintainers): See why attn misbehaves and repair it: logs, incidents, the event bus, database restore.
- [Instances](instances.md) (maintainers): Separate installs of attn under a name, for developing and testing without touching production.
