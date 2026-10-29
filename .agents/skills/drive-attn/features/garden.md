# Garden

The garden is the work tracker: seeds planted, tended and harvested. A seed is
the unit agents hand off, work on and stay organized with, and a plot is a seed
with child seeds. Each profile has one garden. Agents plant seeds, claim them,
log what happened and prove their checks from the CLI. People read the garden
beside their work, search it, move seeds across its board and judge the
checks.

## Actions and ways in

### Open and navigate the garden

⌘⇧T (`board.open`, `app/src/shortcuts/registry.ts`) opens the last frame, initially the drawer;
press it again to switch between drawer and fullscreen, Escape to close. Palette `garden-frame` ("Open the garden") and `show-tended-seeds` ("Show <agent>'s seeds",
offered when the active agent tends a seed or has a stake,
`app/src/application/useAppCommands.tsx`) and the sidebar's "Show the garden" or "Hide the garden"
icon (`app/src/application/useAppSidebarActions.tsx`) also open it. The garden's search, seed and
plot rows, trail and frame button ("Expand the garden" or "Return the garden to the dock") live in
`app/src/components/GardenPanel.tsx`: arrows walk rows, ← climbs out of a plot, fullscreen → drills
in, and `/` returns to search. Fullscreen's "Garden view" picks list or board
(`app/src/components/GardenFrame.tsx`).

### Create and find work

People and agents use `attn seed plant`, `plot`, `ls`, `search`, `ready`, `show`, `edit` and
`export` (`cmd/attn/seed.go`). Plant takes a markdown body and numbered checks; plot creates a
parent and children, parallel unless blocked. Search includes closed seeds; ready lists those
nothing blocks. Export writes markdown for reading and annotation. Outside attn, `--profile <name|id>` picks a profile when several exist.

### Claim, move and hand over work

`attn seed tend`, `park`, `harvest`, `wither` and `replant` manage a seed's state and single tender.
Harvest and wither keep their reason; a park comment goes on the log. `harvest --when-merged [<pr-url>]` arms an automatic harvest, using the session's single open pull request when no URL is
given; `--when-merged --clear` cancels it. The next pull request refresh after a merge harvests an
armed seed (`internal/daemon/garden_harvest_when.go`). On the board, drag a card to Ready, In
progress, Parked or Closed, or use its menu to park, harvest, wither or replant with a reason
(`app/src/components/GardenBoard.tsx`). A seed page offers "Resume", "Handover" and "Send to Chief";
`attn seed handover` is retired and names `attn delegate --seed <id> --handover --cwd <path>`, owned
by [delegation.md](delegation.md). `attn seed send-to-chief` hands it to the chief, owned by
[crew.md](crew.md).

### Read and open a seed

Rows and cards show tender and state, the log shows notes, and the seed chip on an agent's tile
opens its popover (`app/src/components/PaneSeedChip.tsx`). The seed page's "Open as tile" opens a seed tile; an agent can run `attn open <seed-id>` to open one beside itself; `attn ticket` prints `attn seed --help`
(`cmd/attn/main.go`).

### Log, prove and link work

`attn seed notes`, `note` (`--handoff`, `--ring`), `check add|edit|remove|restore|report|confirm|reject|uncheck|reopen`, `link`, `unlink`, `attach` and
`detach` manage the log, checks, blocks/part-of/discovered-from edges and owned files. A handoff
note primes the next tender. The seed page offers "Confirm C1", "Reject…" and "Reopen" for checks.
`watch` and `unwatch` follow a seed and its children; a state move, ringing note or ringing check move, an
armed or cleared harvest condition, or a seed's last blocker closing sends the tender and watchers a
seed update and rings them (`internal/daemon/garden_nudges.go`, `docs/inbox.md`).

### Review and learn the garden

"Review garden" and `attn seed review` invoke the advisor to review growing seeds that went quiet
and possibly draft a handoff (`internal/daemon/garden_advisor.go`,
`internal/prompts/content/garden-advisor/`); Settings, Background agents, Garden advisor chooses
that agent (`app/src/components/GardenAdvisorSettings.tsx`). `attn seed prime` prints the garden
contract included in each session (`internal/prompts/content/garden.md`); `attn seed guide` prints
the craft (`internal/prompts/content/resources/seed-guide.md`). Ready seeds and handoffs arrive at
session start (`internal/prompts/content/session/garden-ready.md`, `garden-row.md`,
`garden-handoff.md`), and seed updates arrive in the inbox
(`internal/prompts/content/session/garden-update.md`).

## Driving it

Preconditions:

- The baseline instance. The steps that run `bridge` also need its app (`bridge --launch --wait-ready ping` answers) with What's new dismissed (`bridge dismiss_whats_new`); the rest need only the daemon.
- The instance's notebook folder exists: `./attn journal append --entry "drive-attn setup"` prints `appended to journal/<date>.md`.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-garden.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, holding `plot.json`: `{"title":"Recipe plot","body":"Two steps.","children":[{"title":"Draft step","blocks":["check-step"]},{"title":"Check step"}]}`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-garden-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "tend", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "tend", "{{seed}}"]}]},
  {"includes": "watch", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "watch", "{{seed}}"]}]},
  {"includes": "merge", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "harvest", "{{seed}}", "--when-merged", "https://mock.github.local/northwind/atlas/pull/412"]}]},
  {"includes": "harvest", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "harvest", "{{seed}}", "-m", "done by the mock agent"]}]},
  {"includes": "arm", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "harvest", "{{seed}}", "--when-merged"]}]},
  {"includes": "clear", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "harvest", "{{seed}}", "--when-merged", "--clear"]}]}
]}
```

`<seed>`, `<plot>`, `<card>`, `<own>` and `<merge>` below are the ids the plant commands print; `<terminal>` is the agent's terminal id from `wsctl list`.

- **Plant.** A person plants a seed with a check from their shell. Run `./attn seed plant "Garden drive" -m "Exercise the garden" --check "C1=Shows in the garden"`. It prints `<seed>  garden-drive  Garden drive` and exits 0; `./attn seed show <seed>` shows `status planted` and C1 open.
- **Plant a plot.** Run `./attn seed plot -f $P/plot.json`. It prints the plot, then `draft-step` and `check-step` with their ids. `./attn seed ready --plot <plot>` lists only `draft-step`, and `./attn seed ls` shows the plot with `1 ready · 1 blocked`.
- **Search, edit and link.** Run `./attn seed search drive`: it prints `1 seed matches "drive"` and names `<seed>` with the line that matched. `./attn seed edit <seed> -m "Exercise every way in"` prints `updated <seed> at revision 2`. `./attn seed link <seed> discovered-from <plot>` prints the edge and `./attn seed unlink <seed> discovered-from <plot>` removes it.
- **Open with the shortcut.** Press ⌘⇧T. Run `bridge native_key '{"key":"t","modifiers":["command","shift"]}'`, then `bridge garden_get_state`. `present` is true, `frame` is `"dock"` (the drawer), and `seeds` lists `<seed>` and `<plot>` with `status: "planted"`.
- **Fullscreen, then close.** Press ⌘⇧T again. `bridge garden_get_state` shows `frame: "full"` and `layout: "columns"`. Run `bridge native_key '{"key":"Escape"}'`: `present` is false.
- **Open from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'` and `bridge native_text '{"text":"Open the garden"}'`. `bridge dom_text '{"selector":".unified-palette-option"}'` starts with "Open the garden". Run `bridge native_key '{"key":"Enter"}'`: `garden_get_state` shows `present: true`.
- **Open from the sidebar.** Close the garden with Escape. Read the icon's box with `bridge dom_bounds '{"selector":"[aria-label=\"Show the garden\"]"}'`, convert it as the README says, and run `bridge native_mouse '{"action":"click","x":<x>,"y":<y>}'`. `garden_get_state` shows `present: true`.
- **Walk with keys.** Press ⌘⇧T until `frame` is `"dock"`. Run `bridge native_key '{"key":"Tab"}'`, then `bridge native_key '{"key":"ArrowDown"}'`. `garden_get_state` names a seed in `focusedRow`. Run `bridge native_key '{"key":"/"}'` and `bridge native_text '{"text":"drive"}'`: `search.focused` is true and `search.results` counts the matches.
- **An agent tends.** Run `wsctl add-session --cwd $A --agent claude --label garden-agent --id garden-agent`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "tend <seed>" --enter`. `./attn agent peek garden-agent` shows `<seed> (garden-drive) is growing, tended by garden-agent`. `./attn seed show <seed>` shows `status growing` and `tender garden-agent`, and `bridge garden_get_state` shows the row with `status: "growing"`.
- **The seed on the agent's tile.** Select the agent for setup with `bridge select_session '{"sessionId":"garden-agent"}'`. `bridge session_seed_chip_get_state '{"sessionId":"garden-agent"}'` shows `present: true`, `id: "<seed>"` and `status: "growing"`. Open the palette as above, type `seeds`, and press Enter on "Show garden-agent's seeds": `bridge dom_wait '{"selector":".tended-seeds-popover","timeoutMs":5000}'` matches and `bridge dom_text '{"selector":".tended-seeds-popover"}'` reads `Growing` and the seed's body.
- **Watch and ring.** Run `wsctl input --id <terminal> --text "watch <plot>" --enter`: peek shows `watching <plot> and its descendants`. From the shell, run `./attn seed note <seed> -m "ring check" --ring`; it prints `rang garden-agent`. Thirty seconds after the last `wsctl input`, `./attn agent peek garden-agent` shows `📬 You have unread items in your attn inbox.`, with the rest of the line wrapped below it.
- **Artifacts.** Run `echo proof > $P/proof.txt` and `./attn seed attach <seed> --path $P/proof.txt --copy`. It prints `copy $P/proof.txt`, `to $HOME/attn-notebook-<name>/seeds/<seed>/proof.txt` with both paths written out, and `markdown target proof.txt`. `./attn seed detach <seed> --path proof.txt --to $P/proof-back.txt` moves it back out.
- **Checks from both sides.** Run `echo proof > $P/proof2.txt`, `./attn seed attach <seed> --path $P/proof2.txt --copy` and `./attn seed check report <seed> C1 -m "seen in the garden" --evidence proof2.txt`; `./attn seed show <seed>` lists the report and `proof2.txt` under C1. Open the garden in the drawer, then click the row `[data-seed-row="<seed>"]` and `[aria-label="Confirm C1"]`, both natively. `bridge dom_wait '{"selector":"[aria-label=\"Uncheck C1\"]","timeoutMs":5000}'` matches, and `./attn seed show <seed> --json` shows C1 with `state: "confirmed"`. `./attn seed check reject <seed> C1 -m "needs a screenshot"` and `./attn seed check reopen <seed> C1` move it back.
- **Seed tile.** Run `./attn open <seed>`. It prints `opened <seed>`, and in `wsctl list` the current desktop's `tree_json` holds a tile whose `tile_kind` is `seed` and whose `tile_params` is `<seed>`. On the seed's page, the "Open as tile" button, clicked natively, does the same.
- **Board.** Plant a card for it: `./attn seed plant "Board drive"` prints `<card>`. Press ⌘⇧T until `frame` is `"full"`, and click `[aria-label="Garden view"] button:last-child` natively. `bridge garden_board_get_state` lists the columns `ready`, `growing`, `parked` and `closed`, with `<card>` under `ready`. Click `[data-seed="<card>"] .garden-card__body` natively: its menu opens on `Park…`. Run `bridge native_key '{"key":"ArrowDown"}'`, `bridge native_key '{"key":"Enter"}'`, `bridge native_text '{"text":"proved from the board"}'` and `bridge native_key '{"key":"Enter"}'`. `./attn seed show <card>` shows `status harvested`.
- **Harvest when merged, with no url.** Run `./attn pr record https://mock.github.local/northwind/atlas/pull/412 --session garden-agent`: it prints `recorded https://mock.github.local/northwind/atlas/pull/412 for session garden-agent`, which makes 412 the session's only open pull request. Plant `<own>` with `./attn seed plant "Own drive"`, then run `wsctl input --id <terminal> --text "arm <own>" --enter`. Peek shows `harvests when northwind/atlas#412 merges`, and so does `./attn seed show <own>`.
- **Take the arming back.** Run `wsctl input --id <terminal> --text "clear <own>" --enter`. Peek shows `harvest-on-merge cleared`. `./attn seed show <own>` keeps `status planted` and drops its `PR last checked` row, and `./attn seed notes <own> --limit 1` lists `harvest-on-merge cleared`. Once peek's `state:` line stops reading `working`, send the same input again: peek shows a `mock agent error:` block that names `has no harvest condition`.
- **Harvest when merged.** Plant `<merge>` with `./attn seed plant "Merge drive"`, then run `wsctl input --id <terminal> --text "merge <merge>" --enter`. Peek shows `harvests when northwind/atlas#412 merges`, and so does `./attn seed show <merge>`. Merge it on the mock GitHub: `curl -s -X POST "$ATTN_MOCK_GH_URL/__control/seed" -H 'content-type: application/json' -d '{"prs":[{"repo":"northwind/atlas","number":412,"title":"Cache the tile layout","state":"closed","merged":true}]}'`. Within one pull request refresh (30 seconds), `./attn seed show <merge>` shows `status harvested` and `reason PR #412 merged: Cache the tile layout`, while `./attn seed show <own>` still shows `status planted`.
- **The agent harvests.** Run `wsctl input --id <terminal> --text "harvest <seed>" --enter`. Peek shows `<seed> (garden-drive) is harvested`, `./attn seed ls` shows it `harvested`, and `bridge garden_board_get_state` lists it under `closed`.
- **Moves from the shell.** `./attn seed replant <seed>` prints `is planted`, `./attn seed park <seed> -m "later"` prints `is dormant`, and `./attn seed wither <seed> -m "drive done"` prints `is withered`. `./attn seed notes <seed> --limit 3` lists the newest entries.
- **Export.** Run `./attn seed export <seed> --out -`. It prints the title, the body and the checks as markdown.
- **Review.** Run `./attn seed review start`. On the baseline it prints the review id with `complete` and `0 seeds`, then `no seeds needed review when this run started`. The "Review garden" button in the garden starts the same review.
- **Guidance and retired verbs.** `./attn seed prime` starts with `attn keeps work as seeds in your profile's garden.`, and `./attn seed guide` prints the craft. `./attn ticket` prints `attn seed --help` and exits 2. `./attn seed handover <seed>` exits 1 naming `attn delegate --seed <id> --handover --cwd <path>`. `./attn seed send-to-chief <seed>` exits 1 with `this profile has no Chief` on the baseline; with a chief it is driven in [crew.md](crew.md), and the seed page's Resume and Handover in [delegation.md](delegation.md).
- **Proof.** In `$P`, keep the output of each `./attn` command with its exit code; `./attn seed show <seed> --json` after the tend, the confirm and the harvest; `./attn seed show <own> --json` after the arm, the clear and the merge; `bridge garden_get_state` and `bridge garden_board_get_state` after each frame change; `bridge capture_screenshot '{"path":"<P>/garden-full.png"}'` with the garden fullscreen and `<P>/seed-chip.png` with the agent's tile, where `<P>` is the folder's path written out, since the JSON is single-quoted; and `./attn agent peek garden-agent` after the tend and after the ring. Put the way in in each file's name, such as `garden-shortcut-state.json`.

## Contexts

- **Durability across restarts**: seeds, logs, checks and artifacts are the daemon's records and come back after any restart. An unread seed update stays in the recipient's inbox, and attn withdraws it when that session stops tending or watching the seed.
- **Linux**: `attn seed` works the same where the daemon and CLI run on Linux. `board.open` is Ctrl+Alt+G there (`LINUX_SHORTCUTS`).
- **Outposts**: an agent on an outpost runs `attn seed` against the home's garden; its attn commands reach the home.
- **Profiles**: a seed belongs to the profile that planted it, for life. Lists, links, children, claims and delegations stay in that profile, and another profile's seed is refused by name. Inside attn the session picks the profile; outside, `--profile` does when there are several.
- **Performance and idle cost**: when the garden holds more seeds than the app keeps, the board says "The garden holds N seeds; this board has the newest M". `app/src/components/gardenSearch.bench.ts` measures search. Nothing measures an open garden's idle cost; when a change touches its rendering, check it for repaints with `docs/perf-testing.md`.
- **Harnesses**: every harness drives the garden with the same `attn seed` verbs and reads the same garden prompts. A seed update rings the tender in its own harness; a shell never gets a ring.
- **Keyboard and native input**: ⌘⇧T, the palette, arrows, `/` and Escape reach the garden and its rows, and a card's open menu takes arrows and Enter. After ⌘⇧T the arrows do nothing until Tab or a click puts focus inside the garden.
- **Compatibility across versions**: a later version reads today's seeds, through the store's garden migrations. Retired verbs answer with their replacement: `attn ticket` and `attn seed handover`.

## Tests

- Wire: `internal/daemon/garden_wire_test.go`, `internal/daemon/garden_*_wire_test.go`, `internal/daemon/seed_*_wire_test.go`.
- Stack: `cmd/attn/seed_stack_test.go`, `cmd/attn/garden_advisor_stack_test.go`, `cmd/attn/ticket_signpost_stack_test.go`, `app/e2e/garden-dock.spec.ts`, `app/e2e/garden-check-tally.spec.ts`, `app/e2e/seed-header.spec.ts`.
- Scenario: `garden-plot-dispatch`, `garden-profile-scope`, `garden-seed-header`, `garden-seed-read-receipts`, `garden-seed-reopen`, `garden-seed-tile-navigation`, `crew-seed-header`.
- Kernel: `internal/garden/*_test.go`, `internal/store/garden_*_test.go`.
- App: `app/src/App.garden*.test.tsx`, `app/src/App.seedArtifactTransfer.test.tsx`, `app/src/App.terminalSeeds.test.tsx`, `app/src/components/PaneSeedChip.test.tsx`, `app/src/components/SeedArtifactRows.test.tsx`, `app/src/components/gardenBoardModel.takeover.test.ts`, `app/src/components/gardenSearch.test.ts`.

## Gotchas

- `tend`, `watch` and arming with `harvest --when-merged` need a session. From a plain shell they exit 1 ("this call named nobody; run it from an attn session, or pass --member <name>", "watching is for a live attn session", "needs a session to track the pull request"). Drive them through the mock agent; `--member` acts as a crew member, which is another door.
- A move on a seed somebody tends is refused, naming the tender, unless it carries `--force`. Plant a fresh seed for shell moves.
- On a fresh instance, `attn seed attach` fails with `notebook root ... no such file or directory` until something creates the notebook folder; the journal append in the preconditions does.
- A new check report clears the verdict. Attach and report first, then confirm.
- Typing through `wsctl input` delays rings by 30 seconds. A `rang <session>` reply only says who will get the update; the proof is the 📬 line in that agent's terminal.
- An ordinary note does not ring; only `--ring` does, along with state moves and the other events in `docs/inbox.md`.
- What's new on a fresh app takes ⌘⇧T: the shortcut seems dead until it is dismissed.
- ⌘⇧T opens the garden in the frame used last, and fullscreen keeps its list or board view across relaunches. A run that opens fullscreen is not a bug.
- `bridge capture_screenshot` leaves out terminal content and the seed chip's popover. Prove those with `./attn agent peek` and `bridge dom_text`.
- `harvest --when-merged` with no url takes the session's one open pull request. With none it exits 1 with `has no open pull request; pass the url`, and with two or more it lists their urls and asks which one. A merged pull request does not count, so the no-url step runs before the merge.
- `harvest --when-merged` fires on the next pull request refresh, not when the mock GitHub changes. Wait for `status harvested`; `wsctl refresh-prs` does not hurry it.
- A review on the baseline finds nothing: it looks at growing seeds whose tender is gone and whose log went quiet, or whose folder was removed. A run with candidates needs copied data, and the advisor is a headless model task, which the baseline refuses.
- Seed ids are random. Take them from the `plant` output or `--json`, never from an earlier run.
- `seed show` pads its labels to the widest row, so `status planted` above stands for the label and its value, not their spacing. A wait loop matches `"status": "harvested"` in `--json`, or a pattern that allows any spacing.
- Working but wrong: `park` and `show` say `dormant` for a parked seed, while the board and `ls` say parked. Escape from fullscreen closes the garden, though the frame button's tooltip says "Return to the dock (Esc)". After ⌘⇧T, focus sits on the frame and the arrows do nothing until Tab; `app/src/App.gardenKeyboard.test.tsx` sends its keys to the garden directly and does not see this.
