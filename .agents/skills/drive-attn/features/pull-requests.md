# Pull requests

Pull requests linked to the sessions that opened them, watched until they are
ready. An agent records the pull request it opens, and attn shows it on the
agent's tile header and keeps its status fresh. An agent can also watch one:
readiness, failed checks and new review feedback arrive in its inbox. Apart
from what agents open, the home screen and the attention drawer list the
user's own pull requests and review requests from GitHub, with approve, merge
and mute.

## Sub-features

- `pr-record`: a session records the pull request it opened; Claude Code and Codex record it from `gh pr create` on their own.
- `pr-forget`: drop a recorded pull request from a session.
- `pr-header`: the recorded pull request on the agent's tile header, with its status and a popover.
- `pr-refresh`: recorded pull requests refresh at a pace that slows as they cool and stops once merged.
- `pr-watch`: a durable watch in green, codex or formal-review mode; updates arrive as inbox notices.
- `pr-wait-ready`: wait in the foreground for the next actionable update, outside the daemon.
- `pr-list`: the user's pull requests and review requests polled from GitHub, on the home screen and in the attention drawer.
- `pr-actions`: approve, merge, mute a pull request, mute its author or its repository, and unmute them in Settings.
- `pr-open`: open a pull request in a new worktree session.
- `pr-guidance`: what attn tells agents whose harness cannot report pull requests.

## How to get to it

### People

- ⌘⇧P (`dock.attention`, labelled "PRs drawer", `app/src/shortcuts/registry.ts`) toggles the attention drawer: sessions needing attention, then "Review Requested" and "Your PRs", each pull request with Approve, Merge and Mute (`app/src/components/AttentionDrawer.tsx`).
- ⌘R (`session.refreshPRs`) polls GitHub for the list now.
- Palette (`app/src/application/useAppCommands.tsx`): `attention` ("Open attention drawer").
- The sidebar dock's "PRs" chip opens the same drawer (`app/src/components/SidebarChrome.tsx`); the dock is set up in [shortcuts.md](shortcuts.md).
- Home screen (`app/src/components/Dashboard.tsx`): the "Yours" and "Review requested" sections. Each card opens the pull request on GitHub and carries "New worktree session", Approve, Merge (with a confirmation), Mute and mute-author buttons (`app/src/components/PRActions.tsx`); a review group's ⊘ mutes the repository. The home screen itself is [home.md](home.md).
- Settings, Attention queue: muted repositories and authors, each with Unmute (`app/src/components/SettingsModalSections.tsx`). Settings, Files and locations: the Projects Directory where "New worktree session" finds the repository.
- Where people see what agents do: the agent's tile header shows `PR`, the repository's name and number with `↗` (`atlas#418 ↗`), and its status. A click opens the pull request's popover with its state, checks, review and merge status, and every pull request the session recorded (`app/src/components/SessionProvenance.tsx`, `app/src/components/SessionPullRequestPopover.tsx`).
- Their own shell: `attn pr` with `--session <id>`, below.

### Agents

- `attn pr` (`cmd/attn/pr.go`, `cmd/attn/pr_session.go`): `record`, `forget`, `ls`, `watch` (`--mode green|codex|formal-review`, `--reviewer`), `unwatch`, `status`, and `wait-ready` (with `--repo`, `--mode`, `--timeout`, `--interval`, `--since`, `--reset`, `--ignore-author`, `--json`). Inside a session the session is implied; outside, `--session <id>` names it. `wait-ready` exits 0 ready, 1 checks failed, 2 usage, 3 changes requested, 4 new feedback, 5 error, merged or closed, and 124 timeout.
- Prompts: the self-report guidance for harnesses without hooks, which asks the agent to run `attn pr record` (`internal/prompts/content/session/pull-request-guidance.md`). A watch's update reaches the agent as an inbox notice, "Pull request monitor: <what happened> for <pull request>." (`internal/daemon/session_pull_request_refresh.go`); how it is read is [inbox.md](inbox.md).

### Triggers

- A Claude Code or Codex agent that runs `gh pr create` has the URL in the command's output recorded on its session (`internal/hooks/pullrequest.go`).
- Recorded pull requests refresh on a 30-second tick: every 30 seconds while watched or recently active, then every 2 and 10 minutes as they cool. A watched pull request's change in readiness or new feedback becomes an inbox notice, which rings the agent; a codex-mode watch is also checked again at its settling deadline (`internal/daemon/session_pull_request_refresh.go`).
- The daemon polls GitHub for the user's pull requests every 90 seconds, on a named instance only with the mock GitHub or `ATTN_GITHUB_POLLING=on` (`internal/daemon/daemon.go`, `internal/daemon/github_polling.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The daemon started with the mock GitHub exports of the env file ([../SKILL.md](../SKILL.md)), so the home screen lists the fixture's pull requests instead of "GitHub polling is off for instance <name>".
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-pull-requests.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Home-screen actions appear only while their row is hovered or focused. Before each action, resolve the point of its `[data-testid="pr-card"]` row with `bridge dom_native_point` and move over it with `bridge native_mouse '{"action":"move","x":<x>,"y":<y>}'`. Then resolve the action button's point and click it. Use `[data-testid="pr-section-yours"] [data-testid="pr-card"]` for the first own pull request, and `[data-testid="pr-section-review"] [data-testid="pr-card"]` for the first review request.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-pr-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`. `unwatch` comes before `watch` because the first turn whose `includes` matches wins:

```json
{"version": 1, "turns": [
  {"includes": "record pr", "actions": [
    {"type": "attn", "args": ["pr", "record", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "unwatch pr", "actions": [
    {"type": "attn", "args": ["pr", "unwatch", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "watch pr", "actions": [
    {"type": "attn", "args": ["pr", "watch", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "forget pr", "actions": [
    {"type": "attn", "args": ["pr", "forget", "https://mock.github.local/northwind/atlas/pull/418"]}]}
]}
```

`<url>` below is `https://mock.github.local/northwind/atlas/pull/418`; `<terminal>` is the agent's terminal id from `wsctl list`.

- **The home screen lists the user's pull requests.** Run `bridge home_get_state`. `prs.yours.rows` lists the fixture's own pull requests by number, `#412` and `#418` among them, and `prs.review.repoGroups` names the repositories with review requests.
- **Open the drawer with the shortcut.** Press ⌘⇧P: `bridge native_key '{"key":"p","modifiers":["command","shift"]}'`, then `bridge dom_wait '{"selector":".attention-drawer[aria-hidden=\"false\"]","timeoutMs":5000}'`. `bridge dom_text '{"selector":".attention-drawer-panel"}'` reads "Needs Attention", "Your PRs" and the fixture's titles. Press ⌘⇧P again: `bridge dom_wait '{"selector":".attention-drawer[aria-hidden=\"true\"]","timeoutMs":5000}'` matches.
- **Open the drawer from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Open attention drawer"}'` and `bridge native_key '{"key":"Enter"}'`. `.attention-drawer[aria-hidden="false"]` appears.
- **Open the drawer from the dock.** Close it with ⌘⇧P, resolve the chip's point with `bridge dom_native_point '{"selector":".sidebar-dock-items [title=\"PRs\"]"}'` and run `bridge native_mouse '{"action":"click","x":<x>,"y":<y>}'`. `.attention-drawer[aria-hidden="false"]` appears. Close it again.
- **Refresh with ⌘R.** Add a pull request on the mock GitHub: `curl -s -X POST "$ATTN_MOCK_GH_URL/__control/seed" -H 'content-type: application/json' -d '{"replace":false,"prs":[{"repo":"northwind/atlas","number":500,"title":"Drive the refresh","roles":["author"],"author":"harness-user"}]}'` prints the new count. Press ⌘R: `bridge native_key '{"key":"r","modifiers":["command"]}'`. `bridge home_get_state` lists `#500` with "Drive the refresh" under `prs.yours.rows`. `wsctl refresh-prs` asks for the same poll over the protocol and prints `PR refresh completed`.
- **Approve, merge and mute.** Read the first row of `prs.yours.rows` from `bridge home_get_state`, then click `[data-testid="pr-section-yours"] [data-testid="approve-button"]` natively. `bridge dom_wait '{"selector":"[data-testid=\"pr-section-yours\"] [data-testid=\"approve-button\"][data-success=\"true\"]","timeoutMs":5000}'` matches, and `curl -s "$ATTN_MOCK_GH_URL/__control"` lists a `POST` to `/repos/<repo>/pulls/<number>/reviews` in `requests`. Click `[data-testid="pr-section-yours"] [data-testid="merge-button"]`, then the dialog's `.merge-confirm-modal .modal-btn-primary` ("Merge"): `requests` gains a `PUT` to `.../merge`, and the row fades and leaves `prs.yours.rows`. Click `[data-testid="pr-section-yours"] [data-testid="mute-button"]`: that row leaves the list too.
- **Mute a repository and an author, then unmute them.** Click the first `[data-testid="pr-section-review"] .repo-mute-btn` natively: its repository leaves `prs.review.repoGroups`. Click the first `[data-testid="pr-section-review"] [data-testid="mute-author-button"]` natively: every review request by that author leaves `prs.review.rows`. Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-hygiene"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-muted-repositories-list\"]"}'` names the repository and `bridge dom_text '{"selector":"[data-testid=\"settings-muted-authors-list\"]"}'` names the author. Click `[data-testid="settings-unmute-repository-button"]` and `[data-testid="settings-unmute-author-button"]` natively, close Settings with Escape, and the group and the author's rows are back in `bridge home_get_state`.
- **An agent records its pull request.** Run `wsctl add-session --cwd $A --agent claude --label pr-agent --id pr-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "record pr" --enter`. `./attn agent peek pr-agent` shows `recorded <url> for session pr-agent`. `./attn pr ls --session pr-agent` lists `mock.github.local/northwind/atlas#418` with `<url>`; within one 30-second refresh its STATE reads `open`.
- **The pull request on the agent's tile.** Select the agent for setup with `bridge select_session '{"sessionId":"pr-agent"}'`, then `bridge dom_wait '{"selector":"[aria-label=\"Pull request atlas#418 details\"]","timeoutMs":5000}'`. Click that button natively: `bridge dom_wait '{"selector":"[role=\"dialog\"][aria-label=\"Pull request atlas#418\"]","timeoutMs":5000}'` matches, and its `dom_text` reads `state` and `open`. Press Escape to close it.
- **The agent watches.** Run `wsctl input --id <terminal> --text "watch pr" --enter`. Peek shows `watching <url> in green mode for session pr-agent; updates arrive in the agent inbox`. After the next refresh, `./attn pr status --session pr-agent` lists the pull request with MODE `green` and HEALTH `delayed`, because the mock GitHub answers no readiness query. Thirty seconds after the last `wsctl input`, `./attn agent peek pr-agent` shows `📬 You have unread items in your attn inbox.`; the item is the notice "Pull request monitor: monitoring is delayed for mock.github.local:northwind/atlas#418."
- **Unwatch and forget.** Run `wsctl input --id <terminal> --text "unwatch pr" --enter`: peek shows `stopped watching <url> for session pr-agent`, and `./attn pr status --session pr-agent` prints `session pr-agent has no watched pull requests`. Run `wsctl input --id <terminal> --text "forget pr" --enter`: peek shows `forgot <url> for session pr-agent`, `./attn pr ls --session pr-agent` prints `session pr-agent has opened no pull requests`, and `[aria-label="Pull request atlas#418 details"]` leaves the tile header (`dom_wait` with `"absent":true`).
- **From the person's shell.** `./attn pr record <url>` exits 2 with `pr record: no session — pass --session <id>, or run inside an attn session`. `./attn pr record <url> --session pr-agent` prints `recorded <url> for session pr-agent`. `./attn pr wait-ready 418` exits 2 with `pr wait-ready: --repo is required when the target is a number`.
- **Guidance.** Run `./attn prompts render session pull-request-guidance`. It prints the text that asks an agent to run `attn pr record <url>` right after `gh pr create`.
- **Open a pull request in a worktree session.** Set Settings, Files and locations, Projects Directory to a folder holding a clone of the pull request's repository, press ⌘⇧H (`session.goToDashboard`, `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`) to return to the home screen, then click a home screen card's `[data-testid="open-button"]` ("New worktree session") natively. The launcher shows its steps, and a session starts in a new worktree on the pull request's branch; the worktree is [worktrees.md](worktrees.md)'s. On the baseline this fails, as Gotchas says. Wait for `.open-pr-launcher` to leave with `bridge dom_wait '{"selector":".open-pr-launcher","absent":true,"timeoutMs":30000}'`, then capture the native alert with `bridge capture_native_window_screenshot '{"path":"<P>/pr-worktree-alert.png"}'`. A web screenshot does not include that alert.
- **Proof.** In `$P`, keep each `./attn pr` command's output with its exit code; `bridge home_get_state` before and after the refresh, the approve, the merge and the mutes; `curl -s "$ATTN_MOCK_GH_URL/__control"` after approve and merge; `./attn agent peek pr-agent` after each agent turn and after the ring; `./attn pr ls --session pr-agent --json` after the record and the forget; `bridge capture_screenshot '{"path":"<P>/pr-drawer.png"}'` with the drawer open and `<P>/pr-header.png` with the agent's tile, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `pr-shortcut-drawer.txt`.

## Contexts

- **Durability across restarts**: recorded pull requests, watches and what each watch already reported, and muted pull requests, repositories and authors are daemon records and come back after any restart. A watch made by a crew member or the chief belongs to its mailbox and outlives the session that made it. `wait-ready` keeps its cursor in the data folder's `pr-wait`, so a later run reports only what is new until `--reset` or `--since`.
- **Linux**: `attn pr` works the same where the daemon and CLI run on Linux. `dock.attention` is Ctrl+Alt+P and `session.refreshPRs` is Ctrl+Shift+R there (`LINUX_SHORTCUTS`).
- **Outposts**: an agent on an outpost runs `attn pr` through its outpost daemon, and its pull request links reach the home like its state and cost.
- **Profiles**: a session's pull requests belong to its session, and so to its profile. The GitHub list on the home screen and in the drawer, and the muted repositories and authors, belong to the daemon: every profile shows the same list.
- **Performance and idle cost**: two sessions on one pull request share one fetch, a rate-limited host is left alone until its limit resets, and a merged pull request is not asked about again. The GitHub list polls every 90 seconds. Nothing measures the drawer's or the header's idle cost; when a change touches their rendering, check it for repaints with `docs/internal/perf-testing.md`.
- **Harnesses**: Claude Code and Codex record a pull request from `gh pr create` on their own. Pi and other plugin harnesses read the self-report guidance and run `attn pr record`. Every harness gets watch notices in its own inbox.
- **Keyboard and native input**: ⌘⇧P toggles the drawer and ⌘R polls. The tile header's popover takes ↑, ↓, Enter (open on GitHub) and `c` (copy the URL) only after a click; while the pointer only hovers, those keys reach the terminal. No macOS menu item claims ⌘⇧P or ⌘R.

## Tests

- Wire: `internal/daemon/session_pull_request*_wire_test.go`, `internal/daemon/pr_*_wire_test.go`, `internal/daemon/github_polling_wire_test.go`, `internal/daemon/gh_warning_wire_test.go`; shared with [automations.md](automations.md): `internal/daemon/automation_review_demand_wire_test.go`, `internal/daemon/automation_review_origin_wire_test.go`.
- Stack: `cmd/attn/pr_stack_test.go`, `app/e2e/pr-actions.spec.ts`.
- Kernel: `internal/github/*_test.go`, `internal/prreadiness/*_test.go`, `internal/hooks/pullrequest_test.go`.
- App: `app/src/App.dashboardPRs.test.tsx`, `app/src/App.openPullRequest.test.tsx`, `app/src/App.sessionProvenance.test.tsx`.

## Gotchas

- The drawer stays in the page while closed, hidden. `.attention-drawer-panel` is always present; wait on `.attention-drawer[aria-hidden="false"]` or `"true"` to tell open from closed.
- The mock GitHub answers no GraphQL. A watch on the baseline always reads HEALTH `delayed`, and its ready, failed-checks and feedback notices cannot be driven live; the wire tests cover them.
- `pr wait-ready` runs the user's own `gh` against real GitHub and never reaches the mock. On the baseline only its usage refusals are drivable.
- A daemon started without the mock exports on a named instance polls nothing: the home screen says "GitHub polling is off for instance <name>" and the drawer lists no pull requests.
- Record URLs on a host attn has a client for. With the mock that is `mock.github.local`; a `github.com` URL records but never refreshes.
- The mock agent runs no harness hook, so `gh pr create` detection cannot be driven with it. Record explicitly; `internal/hooks/pullrequest_test.go` covers detection.
- `attn pr` verbs other than `wait-ready` need a session. From a plain shell, pass `--session <id>`.
- ⌘R and `wsctl refresh-prs` poll the GitHub list only. A session's recorded pull request refreshes on its own 30-second tick.
- "New worktree session" needs a Projects Directory holding a clone it can fetch. The mock serves no Git, so on the baseline it fails with an alert naming the clone or fetch.
- Approve and Merge on the baseline reach only the mock; prove them through its `/__control` `requests`, not through GitHub. Their ✓ (`data-success="true"`) shows for 1.5 seconds, so start the `dom_wait` right after the click.
- Working but wrong: a muted pull request has no way back. Settings lists muted repositories and authors, never single pull requests. The drawer's footer advertises the agent palette's key (⌘K) as its toggle, while its toggle is ⌘⇧P. The same drawer is "PRs drawer" in the shortcut list and "Open attention drawer" in the palette, and titles itself "Needs Attention".
