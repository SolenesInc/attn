# Crew

Permanent agents with charters, who leave letters for their next session; the
chief coordinates the profile. A crew member is an agent with a name, a
charter that says who they are, and a home folder that keeps the charter and
the letters. A member is asleep until something wakes them: the user, an
agent, or an item in their mailbox. Each session of theirs reads the last
letter and closes by filing a new one, which puts the member to sleep, or with
a nap starts their next session from the letters. The chief is the session
that coordinates one profile: seeds sent to the chief land with it, and it
may close or reload any agent of the profile.

## Sub-features

- `crew-roster`: the members of the profile, asleep or awake, with their launch settings.
- `crew-rename`: a member's name, unique in its profile, which the user changes with crew v2 on; its key, the home folder's name, never changes.
- `crew-charter`: who the member is, kept in `CHARTER.md` in the member home and editable from the app.
- `crew-launch`: the folder, harness, model, effort and awareness folders a member's next session starts with; the launch desktop is in [desktops.md](desktops.md).
- `crew-wake`: start a session for an asleep member; asking an awake member returns its session.
- `crew-sleep`: ask the member's session to file its letter and close.
- `crew-letter`: the letter a session files with `attn handoff`, which ends the session and leaves the member asleep, or with `--nap` starts the member's next session from its letters.
- `crew-mailbox`: items addressed to a member reach its current session, and wake it when it sleeps.
- `crew-heartbeat`: attn keeps an idle member's cache warm, and puts it to sleep after the user is away.
- `chief`: the profile's coordinating session; made from the session menu or the new-session picker.
- `send-to-chief`: hand a seed to the chief to decide how it continues.
- `crew-queue`: whether crew members owing a turn wait in the queue.
- `crew-guidance`: what attn tells members and the chief.

## How to get to it

### People

- Palette (`app/src/application/useAppCommands.tsx`): `manage-crew` ("Manage crew"), and `toggle-crew-queue` ("Put the crew in the queue" or "Take the crew out of the queue").
- Sidebar: the "Crew" section with its "manage" button and a row per asleep member whose sun button wakes them on a second click; "Actions for <member>" opens the member's menu (`app/src/components/SidebarChrome.tsx`, `app/src/components/QueueSidebar.tsx`, `app/src/components/QueueRows.tsx`).
- The Manage crew panel (`app/src/components/CrewPanel.tsx`): the member list with "Find a member" (`CrewRoster.tsx`), and per member the tabs "Launch settings" with "Wake" or "Sleep" (`CrewLaunchTab.tsx`), "Charter" (`CrewCharterTab.tsx`), "Handoffs", which lists the letters (`CrewHandoffsTab.tsx`), and "Seeds" (`CrewSeeds.tsx`). With crew v2 on, "Rename" beside the member's name opens a field: Enter saves, Escape cancels. "Close" or Escape closes it.
- The chief: "Make chief of staff" and "Remove chief role" in a session's menu, with a transfer prompt when another session is chief (`app/src/components/SessionActionsPopover.tsx`, `app/src/components/ChiefOfStaffTransferPrompt.tsx`); the chief switch in the new-session picker (`app/src/components/NewSessionDialog/SessionControls.tsx`); the chief badge on its sidebar row.
- A seed's page in the garden: "Send to Chief", with "What should Chief know?" (`app/src/components/GardenPanel.tsx`).
- Settings, Background agents: the chief's model, effort and context budget. Crew v2 has no switch in the app: `attn settings set crew_v2_enabled true`.
- Where people see what members do: a member's session is an ordinary agent tile named after the member; their letters are in the panel's "Handoffs" tab and in the member home.
- Their own shell: `attn crew` and `attn seed send-to-chief`, below.

### Agents

- `attn crew` (`cmd/attn/crew.go`): `list`, `wake <member> [--agent <name>]`, `sleep <member>`, `set <member>` with `--cwd`, `--agent`, `--model`, `--effort`, `--launch-desktop`, `--desktop-name` and `--awareness-dir`, and `rename <member> <name>` (crew v2 only); `--json` and `--profile` on each. A `<member>` is a name in the caller's profile, or `member:<key>`; `attn agent peek`, `msg` and `reload` read member names the same way.
- `attn handoff -m "<letter>"` (`cmd/attn/handoff.go`), with `-m -` to read the letter from stdin, `--nap`, `--retry`, `--session` and `--json`. Plain `attn handoff` files the letter, ends the session and leaves the member asleep, whether or not the user is at the machine; `--nap` ends it and starts the member's next session from its letters instead. After a failed turnover, `--retry` turns over with the letter already filed, asleep unless `--nap` is added.
- `attn seed send-to-chief <id> [-m "<guidance>"]` (`cmd/attn/seed.go`), and `attn seed tend|park|harvest ... --member <name>` to act as a member.
- `attn agent msg <member> "text"` addresses the member's mailbox; [inbox.md](inbox.md) drives it.
- `attn agent reload <member>` reloads the member's current session in place, under the reload rules in [agent-launch.md](agent-launch.md). A sleeping member is refused and stays asleep.
- Prompts: `crew_v2_enabled`, read at each launch, picks the set a member gets. Off: the identity, charter or first day, letters, closure and garden in its session instructions (`internal/prompts/content/crew/`), the opening that wakes it (`crew/wake.md`) and the one a successor gets (`crew/successor.md`). On: the v2 set in `internal/prompts/content/crew/v2/` replaces each of those, openings included (`v2/wake.md`, `v2/successor.md`), and adds the compaction note and the operating model "How you work" (`v2/operating-model.md`). Either way: the sleep request (`crew/sleep-requested.md`, `crew/sleep-away.md`) and the heartbeat (`crew/heartbeat.md`); the chief's guidance (`internal/prompts/content/chief.md`), and its seed assignment (`internal/prompts/content/chief/seed-assignment.md`).
- The attn skill's charter reference, `attn skill --reference charter` (`internal/prompts/content/skills/attn/references/charter.md`), named in its capability index as "Write or update your crew charter" (`internal/prompts/content/skills/attn/SKILL.md`). A v2 member reads it on its first day before writing `CHARTER.md`, and when it adapts its charter to its work. `attn skill` itself is in [agent-guidance.md](agent-guidance.md).

### Triggers

- An item for an asleep member wakes them before the ring, within the wake limit of `crew.wake_limit` wakes per `crew.wake_limit_window_seconds` (`internal/daemon/crew_wake.go`, `internal/daemon/inbox_delivery.go`; rules in `docs/internal/inbox.md`).
- A lifecycle tick every 60 seconds sends an idle member a cache heartbeat before its cache lapses, and asks it to sleep once the user has been away for `crew.away_seconds` (`internal/daemon/crew_lifecycle.go`). `crew.heartbeat_enabled` and `crew.autosleep_enabled` turn each off.

## Driving it

Preconditions:

- The baseline instance. The steps that run `bridge` also need its app (`bridge --launch --wait-ready ping` answers) with What's new dismissed (`bridge dismiss_whats_new`); the rest need only the daemon.
- A member home made before the daemon starts, since a daemon reads new homes only at start: `M="$(./attn instance resolve --field dataDir)/crew/fern" && printf 'export M=%q\n' "$M" >> "$DRIVE_ENV"`, `mkdir -p "$M"` and `printf '# Fern\n\nWait for direction.\n' > "$M/CHARTER.md"`. With a daemon already running, stop it as [../SKILL.md](../SKILL.md) describes (through `"$(./attn instance resolve --field appDaemon)" daemon stop` once the app is installed), then start it again as the baseline does.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-crew.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- A member folder, `C=$(mktemp -d /tmp/drive-attn-crew-agent.XXXX) && echo "export C=$C" >> $DRIVE_ENV`, holding this fixture as `$C/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "unread items", "actions": [
    {"type": "attn", "args": ["agent", "inbox"]}]},
  {"includes": "drive-nap", "actions": [
    {"type": "attn", "args": ["handoff", "--nap", "-m", "Nothing pending. Check the drive seed."]}]},
  {"includes": "drive-sleep", "actions": [
    {"type": "attn", "args": ["handoff", "-m", "Resting. Nothing pending."]}]},
  {"includes": "You have been woken", "actions": [
    {"type": "reply", "text": "Fern here, awake."}]}
]}
```

- A sender for mailbox messages: `wsctl add-session --cwd $C --agent shell --label crew-caller --id crew-caller`.

`<fern>` is the terminal of Fern's current session: the `runtime_id` of the tile in `wsctl list` whose `session_id` is the session `./attn crew list` names. Each wake, nap and mailbox wake starts a session in a new terminal, so read it again after each. Before each send to Fern, re-read `./attn agent peek fern` until its `state:` line stops reading `working`: a turn that is still running holds the ring back.

- **The roster from the shell.** Run `./attn crew list`. Under `MEMBER  STATE  AGENT ...` it lists `Fern  asleep  claude` with its profile and home. Run `./attn crew set fern --cwd $C --agent claude`. It prints `Fern launches in <C> on claude, model ..., effort ...`, `profile: <name>`, `launch desktop: ...` and `awareness dirs: -`.
- **Launch settings from the shell.** Run `./attn crew set fern --awareness-dir $P --launch-desktop 8`. It prints `awareness dirs: <P>` and a `launch desktop:` line for desktop 8. Run `./attn crew set fern --awareness-dir "" --launch-desktop own`: it prints `awareness dirs: -`.
- **Wake from the shell.** Run `./attn crew wake fern`. It prints `Fern is awake in session <id>.` and names `attn agent peek fern`. `./attn crew list` shows `awake` with the session, and `./attn agent peek fern` shows `crew member: this session is Fern today` and `Fern here, awake.` Running the wake again prints `Fern is already awake in session <id> — nothing was launched.`
- **Reload by name.** Run `./attn agent reload fern --source-session crew-caller`: it exits 1 naming Fern's session in a `delegation-chain rule` refusal, since crew-caller is outside its chain. Give the fixture a banner the running process does not have yet: `jq '.banner = "Claude Code mock agent (reload by name)"' "$C/.attn-mock-agent.json" > "$C/next.json" && mv "$C/next.json" "$C/.attn-mock-agent.json"`. Run `./attn agent reload fern --source-session <session>` with the session `./attn crew list --json` names for Fern, a self reload. Re-read `./attn agent peek fern` until it shows the `reload by name` banner on the same session.
- **The member's mailbox.** Run `./attn agent msg fern "hello Fern" --source-session crew-caller`. The receipt is `notified: notified Fern (id <message>)`, and peek on fern shows `📨 from session crew-caller: hello Fern`.
- **Nap into a successor.** Note the session `./attn crew list` names, then run `wsctl input --id <fern> --text "drive-nap" --enter`. The old session closes as it files the letter, so do not look for the mock's reply there. `./attn crew list --json` shows Fern awake in a different session, `ls "$M/handoffs"` lists one letter, and `./attn agent peek fern` names the new session. The successor's opening matches no fixture turn, so it stays silent.
- **Sleep on request.** Read `<fern>` again for the new session. Run `./attn crew sleep fern`. It prints ``Asked Fern in session <id> to write its handoff and file it with `attn handoff`.``, or `Sleep request for Fern is queued in session <id> — ...` while the session is not at a prompt. The ring reaches the session, which reads its inbox: re-read `./attn agent peek fern` until it shows `[attn] The user is asking you to close your session and sleep.` Run `wsctl input --id <fern> --text "drive-sleep" --enter`. The session closes as it files the letter. `./attn crew list` shows `asleep`, and `ls "$M/handoffs"` lists a second letter. `./attn agent reload fern --source-session crew-caller` now exits 1 with `crew member Fern is asleep; reload does not wake members`, and Fern stays asleep.
- **Wake from the mailbox.** With Fern asleep, run `./attn agent msg fern "wake for this" --source-session crew-caller`. The receipt starts `queued: woke Fern in session <id>; notification queued until it reaches a safe prompt`. Read `<fern>` again. The new session opens with `Fern here, awake.`, then reads its inbox: re-read `./attn agent peek fern` until it shows `📨 from session crew-caller: wake for this`.
- **The panel from the palette.** Open the palette as the README says, type `Manage crew` and press Enter. `bridge dom_wait '{"selector":"[data-testid=\"crew-panel\"]","timeoutMs":5000}'` matches. Click `[data-testid="crew-roster-fern"]` natively. On the launch tab, `[data-testid="crew-sleep"]` reads `Sleep` while Fern is awake; click it natively, wait for the request to reach the session as above, then send `drive-sleep` to Fern's terminal. `bridge dom_wait '{"selector":"[data-testid=\"crew-wake\"]","timeoutMs":10000}'` matches, with the button reading `Wake`; click it natively, and `./attn crew list` shows `awake` with a new session.
- **The charter and the letters.** Wake opens Fern's session and closes the panel. Open "Manage crew" from the palette again and click `[data-testid="crew-roster-fern"]` natively. Click `[data-testid="crew-tab-charter"]` natively, then click `[data-testid="crew-charter-editor"]` natively. Run `bridge native_key '{"key":"ArrowDown","modifiers":["command"]}'`, `bridge native_key '{"key":"Enter"}'` and `bridge native_text '{"text":"Keep the drive notes short."}'`. `bridge dom_wait '{"selector":"[data-testid=\"crew-charter-status\"]","textIncludes":"Saved","timeoutMs":5000}'` matches, and `cat "$M/CHARTER.md"` ends with the new line. Click the "Handoffs" tab, `[data-testid="crew-tab-handoffs"]`, natively. It lists the letters as `[data-testid="crew-handoff-0"]` and onward; click it natively and `[data-testid="crew-handoff-reader"]` shows a letter's text: `Nothing pending. Check the drive seed.` or `Resting. Nothing pending.` Close the panel with Escape.
- **Wake from the sidebar.** Put Fern to sleep again. In the sidebar's Crew section, click `[data-testid="queue-crew-wake-fern"]` natively: the row shows `confirm`. Click it again within four seconds: `./attn crew list` shows `awake`. The section's `[data-testid="manage-crew"]` button opens the same panel.
- **Wake with another harness.** Put Fern to sleep as above, then run `./attn crew wake fern --agent codex`. Read `<fern>` again. `./attn agent peek fern` shows `session <id> (codex) — Fern` and `Fern here, awake.`
- **Rename.** With crew v2 off, `./attn crew rename fern Ferdinand` exits 1 naming `crew_v2_enabled`. Run `./attn settings set crew_v2_enabled true`, then `./attn crew rename fern chief`: it refuses, `"chief" is reserved`. `./attn crew rename fern Ferdinand` prints `Fern is now Ferdinand; its key member:fern, home, letters and mail stay.` `./attn crew list` shows `Ferdinand` with the same home, `./attn crew wake fern` refuses with `no crew member named "fern" in profile ...`, and `./attn crew wake member:fern` reaches Ferdinand. With Ferdinand awake, `./attn agent list` and its sidebar row name it Ferdinand. In the app, open Manage crew, click `[data-testid="crew-rename"]` natively, type a name and press Enter: the heading and `./attn crew list` show it. Set `crew_v2_enabled` back to false when done.
- **Crew v2.** Run `./attn prompts render crew wake`: it prints `You have been woken for today. ...`. Run `./attn prompts render crew wake --set crew_v2_enabled=true`: it prints `You have been woken. Get up to speed, ...`. `grep -l "## How you work" $C/.attn-mock-agent/*.jsonl` lists no file, since no launch so far carried the v2 prompts. Put Fern to sleep as above, run `./attn settings set crew_v2_enabled true`, then `./attn crew wake fern`. Read `<fern>` again: `./attn agent peek fern` shows `Fern here, awake.`, and the same `grep -l` now lists one launch record, the new session's. Run `./attn settings set crew_v2_enabled false`; Fern's session keeps what it launched with.
- **The charter reference.** Run `./attn skill --reference charter`. It prints `# Write your charter`, then what a charter holds, from `- Who you are: your name, character, and what matters to you.` to `- The user's lasting preferences that guide how you work together.` The skill does not depend on `crew_v2_enabled`, so this works on the baseline.
- **Make a chief.** Run `wsctl add-session --cwd $C --agent claude --label chief-agent --id chief-agent --place`. Click `[data-testid="session-actions-chief-agent"]` natively, then `[data-testid="chief-of-staff-session-action"]`, which reads `Make chief of staff`. `bridge chief_of_staff_get_state` shows `chiefOfStaff: true` for chief-agent, and its sidebar row carries `.chief-of-staff-badge`.
- **Send a seed to the chief.** Plant a seed: `./attn seed plant "Chief drive"` prints `<seed>  <slug>  Chief drive`. Run `./attn seed send-to-chief <seed> -m "Decide where this goes."`. It prints `<seed> sent to Chief (...)`. The ring reaches chief-agent, which reads its inbox: peek on chief-agent shows ``The user sent seed `<seed>` to you``, and `./attn seed show <seed>` names chief-agent as tender. On another seed's page in the garden, "Send to Chief" opens "What should Chief know?" and its submit button does the same.
- **The crew in the queue.** Open the palette, type `crew in the queue` and press Enter on "Put the crew in the queue". `./attn settings get queue_crew_enabled` prints `true`. Open the palette again, type `crew out of the queue` and press Enter on "Take the crew out of the queue": the setting reads `false`.
- **Heartbeat.** With Fern awake and idle after at least one turn, run `./attn settings set crew.cache_ttl_seconds 60` and `./attn settings set crew.heartbeat_lead_seconds 30`, the smallest values the daemon accepts. Within two lifecycle ticks (about two minutes), re-read `./attn agent peek fern` until it shows `[attn] This is a cache heartbeat.` typed into the session. Set the keys back to their defaults with `./attn settings set crew.cache_ttl_seconds 3600` and `./attn settings set crew.heartbeat_lead_seconds 300`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code; `./attn crew list --json` after each wake and sleep; `./attn agent peek fern` after each ring and each letter; `ls "$M/handoffs"` and `cat "$M/CHARTER.md"`; the `grep -l "## How you work"` output before and after the v2 wake; `bridge chief_of_staff_get_state`; and `bridge capture_screenshot '{"path":"<P>/crew-panel.png"}'` with the panel open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `crew-sidebar-wake-list.json`.

## Contexts

- **Durability across restarts**: charters and letters are files in the member home, and the roster, launch settings and bindings are the daemon's records. An awake member's session comes back like any session, and unread mailbox items stay for the member's next session.
- **Linux**: `attn crew` and `attn handoff` work the same where the daemon and CLI run on Linux. The crew has no shortcut; the palette reaches it.
- **Outposts**: a member's sessions start on the home's machine; `attn crew set` has no host. A session on an outpost has no "Make chief of staff" in its menu.
- **Profiles**: every member belongs to one profile and wakes only there. Names resolve only in the caller's profile (the calling session's, else `--profile`, else the app's selected profile), so a member of another profile answers as if it did not exist, and two profiles can each have a Bob. Each profile has at most one chief, and a message to a member of another profile answers as if the member did not exist.
- **Performance and idle cost**: the sidebar's wake sun animates to a held frame when armed and stops; a glow that keeps moving is a repaint defect. Each heartbeat is a turn typed into an idle member's session before its cache lapses: 3600 seconds for Claude and 1800 for Codex unless `crew.cache_ttl_seconds` says otherwise, less `crew.heartbeat_lead_seconds`.
- **Harnesses**: `attn crew set --agent` picks claude, codex or an installed plugin driver, and `attn crew wake --agent` overrides it for one session. With `crew_v2_enabled`, the v2 prompts under Agents and harness memory settings apply at the next launch: Claude Code gets "How you work" in its appended system instructions, and Codex, plain or shared, in its developer instructions.
- **Keyboard and native input**: the palette opens the panel, Escape closes it, and the wake sun is a button that Enter or Space arms and then confirms.
- **Compatibility across versions**: crew memory was removed; with `crew_v2_enabled` on, a letter also records its session so attn keeps that session's transcript. The store's crew migrations carry older member records forward. `attn handoff --sleep` is retired and exits 2 with `flag provided but not defined: -sleep`. A member session an earlier build started was told to file with it; plain `attn handoff` now does the same, and from such a session it sleeps even where its instructions promised a successor while the user is present.
- **Flow**: in desktop flow, the sidebar lists asleep members above the desktops. In queue flow, the queue sidebar has a crew block with awake and asleep members, and "Put the crew in the queue" lets a member owing a turn wait in the queue.

## Tests

- Wire: `internal/daemon/crew_*_wire_test.go`, `internal/daemon/crew_wire_test.go`, `internal/daemon/chief_of_staff_wire_test.go`, `internal/daemon/chief_profiles_wire_test.go`, `internal/daemon/chief_nudge_quiet_wire_test.go`, `internal/daemon/create_as_chief_wire_test.go`, `internal/daemon/plugin_chief_wire_test.go`, `internal/daemon/seed_send_to_chief_wire_test.go`, `internal/daemon/garden_handoff_wire_test.go`, `internal/daemon/profile_delete_crew_wire_test.go`, `internal/daemon/crew_wake_test.go`.
- Stack: `cmd/attn/crew_stack_test.go`, `cmd/attn/handoff_stack_test.go`, `cmd/attn/crew_day_transcript_stack_test.go`, `scripts/verify-handoff.mjs`, `scripts/verify-crew-v2.mjs`, `scripts/verify-crew-operating-model.mjs`.
- Scenario: `crew-management`, `crew-seed-header`.
- Kernel: `internal/crew/lifecycle_test.go`, `internal/crew/priming_test.go`, `internal/crew/prompt_compatibility_test.go`.
- App: `app/src/App.chiefOfStaff.test.tsx`, `app/src/App.crewCharter.test.tsx`, `app/src/App.crewLaunchSettings.test.tsx`, `app/src/App.crewRoster.test.tsx`, `app/src/App.crewWake.test.tsx`, `app/src/components/CrewPanel.test.tsx`, `app/src/utils/crewName.test.ts`, `app/src/application/useChiefOfStaff.profile.test.tsx`, `app/src/application/useAppSessions.notebookChief.test.tsx`.

## Gotchas

- A member joins the roster only when the daemon starts and finds `<name>/CHARTER.md` in the instance's crew folder. A home made while the daemon runs stays invisible until the next start. Member names are lowercase letters, digits and `-`, starting with a letter.
- Both wake openings mention the member's letter, so a fixture turn that matches `letter` fires at wake. Match on words the opening does not carry, as the fixture above does.
- `crew_v2_enabled` changes the openings. The v2 wake starts `You have been woken. Get up to speed` and carries no `woken for today` or `predecessor`; the v2 successor starts `Your previous session filed the letter above` instead of `Your predecessor closed their day`. A turn that matches words only one set carries never fires under the other, and the run silently proves nothing. Match words both carry, such as `You have been woken` and `the letter above`, as the fixture's wake turn does.
- A sleep request is an inbox notice: the member reads it with `attn agent inbox` after the 📬 ring, and the 30 seconds of quiet after any `wsctl input` to that terminal apply. Only the member's own `attn handoff` ends the session; `attn crew sleep` and the panel's Sleep only ask.
- Plain `attn handoff` leaves the member asleep even while the user is at the machine, so a fixture turn or a run that expects a successor passes `--nap`. `attn handoff --sleep` exits 2 with `flag provided but not defined: -sleep`, which shows as a `mock agent error:` block while the session stays awake.
- Inbox wakes count against `crew.wake_limit`; a run that wakes a member by message many times hits it, and the receipt names the setting. Wakes from the sidebar or `attn crew wake` do not count.
- attn never wakes the chief. Items for the chief wait for the next chief, and on a profile without one `attn seed send-to-chief` exits 1 with `this profile has no Chief`.
- The panel's Wake stays disabled until the launch settings are saved.
- Wake selects the member's session and closes Manage crew. Reopen the panel before looking for its charter or letters. The closed panel's root remains in the DOM with zero-size bounds; its presence alone does not show that the panel is open.
- The CLI and the panel still use older words for a member's session and letters (the "Handoffs" tab, `attn handoff`, the sleep request's "handoff"). Match the literal text shown here; the glossary lists those words as debt, not as a failure of the run.
- Working but wrong: `Asked Fern ...` and `Sleep request for Fern is queued ...` only say the request was recorded. Fern is asleep when `./attn crew list` says so. Likewise `awake` says a session launched, not that it read its charter: prove a wake by the member's reply in `./attn agent peek fern`.
