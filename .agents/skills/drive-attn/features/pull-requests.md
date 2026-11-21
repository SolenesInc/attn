# Pull requests

Pull requests linked to the sessions that opened them, watched until they are
ready. An agent records the pull request it opens, and attn shows the pull
request on the agent's tile header and keeps the pull request's status fresh.
An agent can also watch a pull request: readiness, failed checks and new review
feedback arrive in the agent's inbox. Apart from the pull requests that agents
open, the home screen lists the user's own pull requests and review requests
from GitHub, with approve, merge and mute.

## Sub-features

- `pr-record`: a session records the pull request it opened. Claude Code and Codex record the pull request from `gh pr create` on their own.
- `pr-forget`: drop a recorded pull request from a session.
- `pr-header`: the recorded pull request on the agent's tile header, with its status and a popover.
- `pr-refresh`: attn refreshes a recorded pull request less often as it cools, and stops once the pull request is merged.
- `pr-watch`: a durable watch in green, codex or formal-review mode. Updates arrive as inbox notices.
- `pr-wait-ready`: wait in the foreground for the next actionable update, outside the daemon.
- `pr-list`: the user's pull requests and review requests polled from GitHub, on the home screen.
- `pr-actions`: approve, merge, mute a pull request, mute its author or its repository, and unmute them in Settings.
- `pr-open`: open a pull request in a new worktree session.
- `pr-guidance`: what attn tells agents whose harness cannot report pull requests.

## How to get to it

### People

- ⌘R (`session.refreshPRs`, `native/crates/attn-app/src/keymap.rs`) polls GitHub for the list now.
- Home screen's Pull Requests card, `home.prs` (`native/crates/attn-app/src/home/pull_requests.rs`): "Yours", then "Review requested" grouped by repository, each group, `home.prs.repo.<repo>`, folding (`.fold`) and muting its repository (`.mute`) from its header. A card row, `home.pr.<id>`, opens with its role, `.role` ("Yours", "Review requested", or "Review requested by a bot" when the author's name contains "bot"), names its GitHub host in `.open`'s name ("#4 on ghe.example: …") when its repository's pull requests span more than one host, opens the pull request on GitHub and marks it visited, shows `.updated`, `.approved` and `.ci` marks, dims once the user approved it with no new changes, and carries New worktree session (`.new`), Approve (`.approve`), Merge (`.merge`, with a confirmation, `home.prs.merge-dialog`), Mute (`.mute`) and the author's name (`.mute-author`, "Mute all PRs by <author>"). `home.prs.refresh` polls. While the daemon reports GitHub rate-limiting its polls, `home.rate-limit` above the cards counts down, "GitHub rate limited. Resuming in <m>m <s>s". The recipe for the home screen is in [home.md](home.md).
- **Settings → Attention queue**: Muted Repositories and Muted Authors, `settings.muted.repo.<name>` and `settings.muted.author.<name>`, each with Unmute, `settings.unmute.<kind>.<name>` (`native/crates/attn-app/src/settings/hygiene.rs`). **Settings → Files and locations**: the Projects Directory where "New worktree session" finds the repository.
- **Settings → Agents and models**: the "Ring on my own PR comments" switch, `settings.toggle.pull_requests.own_comments`, which saves `pull_requests.own_comments` as `ring` or `quiet` (`native/crates/attn-app/src/settings/agents.rs`).
- Where people see what agents do: the agent's tile header shows the pull request, `tile.<pane>.details`, named "Pull request <repo>#<number> details". A press opens the pull request's popover, `tile.<pane>.details.popover`, a dialog named "Pull request <repo>#<number>", with its state, checks, review and merge status, and every pull request the session recorded (`native/crates/attn-app/src/desktop/provenance.rs`). A sidebar row shows the same pull request as `<row>.pr`.
- Their own shell: `attn pr` with `--session <id>`, below.

### Agents

- `attn pr` (`cli/attn/src/commands/pr/`, `internal/catalog/content/pr.yaml`, and `cmd/attnd/pr.go` for `wait-ready`): `record`, `forget`, `ls`, `watch` (`--mode green|codex|formal-review`, `--reviewer`), `unwatch`, `status`, and `wait-ready` (with `--repo`, `--mode`, `--timeout`, `--interval`, `--since`, `--reset`, `--ignore-author`, `--json`). Inside a session, the session is implied. Outside a session, `--session <id>` names the session. `wait-ready` exits 0 ready, 1 checks failed, 2 usage, 3 changes requested, 4 new feedback, 5 error, merged or closed, and 124 timeout.
- The tools `pr_record`, `pr_forget`, `pr_ls`, `pr_watch`, `pr_unwatch` and `pr_status` when `agent_tools_enabled` is on. They act as the calling session and take no session. `wait-ready` is not a tool: `pr_watch` is its form, and its updates arrive in the inbox.
- `attn settings get|set pull_requests.own_comments` (`quiet` or `ring`; unset reads empty and means quiet), a profile setting ([settings.md](settings.md)).
- Prompts: the self-report guidance for harnesses without hooks, which tells the agent to run `attn pr record` (`internal/prompts/content/session/pull-request-guidance.md`). A watch's update reaches the agent as an inbox notice, "Pull request monitor: <what happened> for <pull request>." (`internal/daemon/session_pull_request_refresh.go`). See [inbox.md](inbox.md) for how the agent reads the notice.

### Triggers

- When a Claude Code or Codex agent runs `gh pr create`, attn records the URL in the command's output on the agent's session (`cli/attn/src/hooks/detect.rs`).
- Recorded pull requests refresh on a 30-second tick: every 30 seconds while watched or recently active, then every 2 and 10 minutes as they cool. When a watched pull request changes readiness or gets new feedback, attn creates an inbox notice, and the notice rings the agent. Feedback that the GitHub account attn uses for the host wrote, and reopened threads that account started on its own pull request, make no notice unless the watching session's profile sets `pull_requests.own_comments` to `ring`. State changes always make one. attn also checks a codex-mode watch again at the watch's settling deadline. When GitHub has answered on every refresh for 10 minutes that it cannot find the pull request (GraphQL `NOT_FOUND` on the repository or pull request, or a 404 on the pull request's REST read), attn sends each watcher a last "watch ended" notice, removes the watches and records the pull request closed, so it is never asked about again; any other failure, a 404 from the GraphQL endpoint included, is a delay (`internal/daemon/session_pull_request_refresh.go`).
- The daemon polls GitHub for the user's pull requests every 90 seconds, on a named instance only with the mock GitHub or `ATTN_GITHUB_POLLING=on` (`internal/daemon/daemon.go`, `internal/daemon/github_polling.go`).

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, then `bridge wait '{"target":{"key":"home"}}'`. Start the daemon with the mock GitHub exports of the env file ([../SKILL.md](../SKILL.md)), so the home screen lists the fixture's pull requests instead of "GitHub polling is off for instance <name>". On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`, and use the Linux chords: `mod+shift+r` to refresh, `mod+shift+,` for Settings and `mod+alt+h` for home.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-pull-requests.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Each `bridge` call takes a few seconds. Wait for a short-lived state with `bridge wait` on the role and name, as below, instead of reading the tree after a sleep.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-pr-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`. `unwatch` comes before `watch` because the first turn whose `includes` matches wins:

```json
{"version": 1, "turns": [
  {"includes": "record pr", "actions": [
    {"type": "attn", "args": ["pr", "record", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "unwatch pr", "actions": [
    {"type": "attn", "args": ["pr", "unwatch", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "watch pr", "actions": [
    {"type": "attn", "args": ["pr", "watch", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "forget pr", "actions": [
    {"type": "attn", "args": ["pr", "forget", "https://mock.github.local/northwind/atlas/pull/418"]}]},
  {"includes": "watch codex", "actions": [
    {"type": "attn", "args": ["pr", "watch", "https://mock.github.local/northwind/atlas/pull/418", "--mode", "codex"]}]},
  {"includes": "watch formal", "actions": [
    {"type": "attn", "args": ["pr", "watch", "https://mock.github.local/northwind/atlas/pull/418", "--mode", "formal-review", "--reviewer", "dana-park"]}]},
  {"includes": "watch bad", "actions": [
    {"type": "attn", "args": ["pr", "watch", "https://mock.github.local/northwind/atlas/pull/418", "--mode", "codex", "--reviewer", "dana-park"]}]},
  {"includes": "status pr", "actions": [
    {"type": "attn", "args": ["pr", "status", "--json"]}]}
]}
```

`<url>` below is `https://mock.github.local/northwind/atlas/pull/418`. `<terminal>` is the agent's terminal id from `wsctl list`.

- **The home screen lists the user's pull requests.** Press `mod+shift+h` so the home screen shows. `bridge tree` holds `home.prs` with the fixture's own pull requests under `home.prs.yours`, `#412` and `#418` among them, and `home.prs.review` with a `home.prs.repo.<repo>` group for each repository with review requests.
- **Refresh with ⌘R.** Add a pull request on the mock GitHub: `curl -s -X POST "$ATTN_MOCK_GH_URL/__control/seed" -H 'content-type: application/json' -d '{"replace":false,"prs":[{"repo":"northwind/atlas","number":500,"title":"Drive the refresh","roles":["author"],"author":"harness-user"}]}'` prints the new count. Press `mod+r`: `home.prs.refresh` is named "Refreshing PRs" and disabled until the daemon answers, and the `requestCount` in `curl -s "$ATTN_MOCK_GH_URL/__control"` grows. `bridge tree` then lists `#500` with "Drive the refresh" under `home.prs.yours`. A failed poll turns the button into an error mark whose description is the reason, and the next refresh clears it. `wsctl refresh-prs` asks for the same poll over the protocol and prints `PR refresh completed`.
- **Approve.** Press a review request's `home.pr.<id>.approve`: `bridge wait '{"target":{"role":"button","name":"Approved"}}'` answers, and `requests` in `/__control` gains a `POST` to that pull request's `/reviews`. The button reads "Approve" again 1.5 s later. Once the daemon marks it approved, the row shows `.approved` and dims, and `home.prs.count` drops by one.
- **Merge asks first.** Press one of your pull requests' `home.pr.<id>.merge`: `home.prs.merge-dialog` shows, a dialog named "Merge PR #<number>?". Press Escape: the dialog goes and the Merge button holds the keyboard. Press Merge again, then `home.prs.merge.confirm`: `bridge wait '{"target":{"role":"button","name":"Merged"}}'` answers, `requests` gains a `PUT` to its `/merge`, and the row leaves the card once its check has shown.
- **Mute.** Press a pull request's `home.pr.<id>.mute`: it leaves the card at once. Mute toggles in the daemon, so the row stays hidden until the daemon reports the pull request muted.
- **Mute a repository and an author, then unmute them.** Press a review group's `home.prs.repo.<repo>.mute`: its group leaves `home.prs.review`. Press a review request's `home.pr.<id>.mute-author`: every pull request by that author leaves the card, and the daemon log has `mute_author`. Open Settings with `mod+,` and press `settings.nav.hygiene`: `settings.muted.repo.<repo>` names the repository and `settings.muted.author.<author>` the author. Press `settings.unmute.repo.<repo>` and `settings.unmute.author.<author>`, then close Settings with `Escape`. The group and the author's rows are back under `home.prs`.
- **Home's card marks a visit.** Press a review request's `home.pr.<id>.open`: the daemon log has `Marking PR <id> as visited`. Seed the mock GitHub again with `replace:true`, every pull request the recipe uses in one payload and this one with a new `headSHA`, and press `home.prs.refresh`: `bridge wait` on `home.pr.<id>.updated` answers, named "Updated". Press `.open` again: `.updated` goes.
- **Home's card marks approval and CI.** A row the user approved, by pressing its Approve or from a seeded `reviews` entry of `APPROVED`, shows `.approved` and dims. `.ci` follows the mock's `mergeableState`: `clean` reads "CI success", `blocked` or `unstable` "CI pending", and `dirty` "CI failure".
- **An agent records its pull request.** Run `wsctl add-session --cwd $A --agent claude --label pr-agent --id pr-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "record pr" --enter`. `./attn agent peek pr-agent` shows `recorded <url> for session pr-agent`. `./attn pr ls --session pr-agent` lists `mock.github.local/northwind/atlas#418` with `<url>`. Within one 30-second refresh, the row's STATE reads `open`.
- **The pull request on the agent's tile.** Press `sidebar.session.pr-agent`. `<pane>` is the `pane_id` of its pane in `bridge state '{"of":"desktops","includes":"pr-agent"}'`. `bridge wait '{"target":{"key":"tile.<pane>.details"}}'` answers, named "Pull request atlas#418 details". Press it: `bridge wait '{"target":{"key":"tile.<pane>.details.popover"}}'` answers, a dialog named "Pull request atlas#418", and its rows in `bridge tree` read `open`. Press `Escape` to close it.
- **The agent watches.** Run `wsctl input --id <terminal> --text "watch pr" --enter`. Peek shows `watching <url> in green mode for session pr-agent; updates arrive in the agent inbox`. After the next refresh, `./attn pr status --session pr-agent` lists the pull request with MODE `green` and HEALTH `delayed`, because the mock GitHub answers no readiness query. Thirty seconds after the last `wsctl input`, `./attn agent peek pr-agent` shows `📬 You have unread items in your attn inbox.` The item is the notice "Pull request monitor: monitoring is delayed for mock.github.local:northwind/atlas#418."
- **The other watch modes.** Run `wsctl input --id <terminal> --text "watch codex" --enter`: peek shows `watching <url> in codex mode for session pr-agent`. Run `wsctl input --id <terminal> --text "watch formal" --enter`: peek shows `in formal-review mode`, and `wsctl input --id <terminal> --text "status pr" --enter` prints JSON with `"watch_mode": "formal-review"` and `"watch_reviewer": "dana-park"`. Run `wsctl input --id <terminal> --text "watch bad" --enter`: a `mock agent error:` block ends `--reviewer is only valid with --mode formal-review`.
- **Unwatch and forget.** Run `wsctl input --id <terminal> --text "unwatch pr" --enter`: peek shows `stopped watching <url> for session pr-agent`, and `./attn pr status --session pr-agent` prints `session pr-agent has no watched pull requests`. Run `wsctl input --id <terminal> --text "forget pr" --enter`: peek shows `forgot <url> for session pr-agent`, `./attn pr ls --session pr-agent` prints `session pr-agent has opened no pull requests`, and `bridge wait '{"target":{"key":"tile.<pane>.details"},"gone":true}'` answers: the pull request leaves the tile header.
- **A closed session's watch.** Record and watch `<url>` with `--session <id>` from outside the agent, then close the session with `./attn agent close <id> -m "done" --source-session <id>`. `./attn pr ls --session <id>` still lists the pull request with MODE `green`, `./attn pr unwatch <url> --session <id>` prints `stopped watching <url> for session <id>`, and `./attn pr status --session <id>` then prints `session <id> has no watched pull requests`. An id the instance never had fails both with `unknown session <id>`. An agent in another profile (`wsctl add-session --profile <id>`) running `attn pr ls`, `watch` or `unwatch` with `--session <id>` gets the same `unknown session <id>`, for a closed or a live session.
- **A harness reports `gh pr create`.** The mock runs no hook, so call the one Claude Code would, as the README allows: with `<terminal>` written out, run `printf '%s' '{"tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":{"stdout":"https://mock.github.local/northwind/atlas/pull/418"}}' | ./attn _hook-tool-use <terminal>`. It exits 0, and `./attn pr ls --session pr-agent` lists `mock.github.local/northwind/atlas#418` again. Forget it with `./attn pr forget <url> --session pr-agent`.
- **From the person's shell.** `./attn pr record <url>` exits 2 with `pr record: no session — pass --session <id>, or run inside an attn session`. `./attn pr record <url> --session pr-agent` prints `recorded <url> for session pr-agent`. `./attn pr wait-ready 418` exits 2 with `pr wait-ready: --repo is required when the target is a number`.
- **Guidance.** Run `./attn prompts render session launch --set self_report_pull_requests=true`. It prints launch instructions with the text that asks an agent to run `attn pr record <url>` right after `gh pr create`. Without the `--set`, that text is absent.
- **Open a pull request in a worktree session.** Set `./attn settings set projects_directory <dir>` to a folder holding a clone, named for the repository and set up as Gotchas says, whose `origin` has the pull request's `headRef` and no local branch of that name. Press `home.pr.<id>.new`: the corner `worktree-setup` names each step, then the agent starts in `<dir>/<repo>--<branch>`, labelled `<repo>#<number>`, and `home` goes. The recipe for the worktree is in [worktrees.md](worktrees.md). With no clone, the mock's clone URL does not resolve: the corner reads "Create failed: <repo>#<number> failed", and pressing it opens the reason.
- **Ring on my own PR comments.** Open Settings with `mod+,` and press `settings.nav.agents`. In `bridge tree`, `settings.toggle.pull_requests.own_comments` reads unchecked, and `./attn settings get pull_requests.own_comments` prints an empty line while the setting is unset. Press the switch: it reads checked and the setting prints `ring`. Press it again: the setting prints `quiet`. Run `./attn settings set pull_requests.own_comments ring`: the switch reads checked. The mock GitHub answers no GraphQL, so the notices that the setting decides stay with the wire test, as Gotchas says for the other notices.
- **Proof.** In `$P`, keep these:
  - each `./attn pr` command's output with its exit code
  - `./attn settings get pull_requests.own_comments` after each press of the switch and after the shell's `set`
  - `bridge tree` with Home showing before and after the refresh, the approve, the merge and the mutes
  - `curl -s "$ATTN_MOCK_GH_URL/__control"` after approve and merge
  - `./attn agent peek pr-agent` after each agent turn and after the ring
  - `./attn pr ls --session pr-agent --json` after the record and the forget
  - `bridge shot '{"path":"<P>/pr-home.png"}'` with an Updated and an approved row showing, `<P>/pr-header.png` with the agent's tile, and another shot with a failed worktree corner, where `<P>` is the folder's path written out

  Name each file for its way in, such as `pr-refresh-key.txt`.

## Contexts

- **Durability across restarts**: recorded pull requests, watches and what each watch already reported, and muted pull requests, repositories and authors are daemon records and come back after any restart. A watch made by a crew member or the chief belongs to the mailbox of the crew member or chief who made it, and outlives the session that made it. `wait-ready` keeps its cursor in the data folder's `pr-wait`, so a later run reports only what is new until `--reset` or `--since`.
- **GitHub hosts**: a refresh or poll changes the list only for the hosts it heard from. A pull request of a host that discovery has not found yet, whose token `gh` refuses, or that `gh auth status` reports as failing keeps its row and its mute. Only a host missing from a `gh auth status` that lists other hosts loses its rows. When `gh` lists no hosts at all, the list stays as it was.
- **Linux**: `attn pr` works the same where the daemon and CLI run on Linux. `session.refreshPRs` is Ctrl+Shift+R there (the `LINUX` table in `native/crates/attn-app/src/keymap.rs`).
- **Outposts**: an agent on an outpost runs `attn pr` through its outpost daemon, and the agent's pull request links reach the home like the agent's state and cost.
- **Profiles**: a session's pull requests belong to the session, and so to the session's profile. The GitHub list on the home screen, and the muted repositories and authors, belong to the daemon: every profile shows the same list. A watch reads `pull_requests.own_comments` from the profile of the session that recorded the pull request.
- **Performance and idle cost**: two sessions on one pull request share one fetch, attn sends no request to a rate-limited host until the host's limit resets, and attn does not ask about a merged pull request again. The GitHub list polls every 90 seconds. Nothing measures the idle cost of the home screen's list or the header. When a change touches the rendering of either, check the rendering for repaints with `docs/internal/perf-testing.md`.
- **Harnesses**: Claude Code and Codex record a pull request from `gh pr create` on their own. Plugin harnesses read the self-report guidance and run `attn pr record`. Every harness gets watch notices in its own inbox.
- **Keyboard and native input**: ⌘R polls. The tile header's popover takes ↑, ↓, Enter (open on GitHub) and `c` (copy the URL) only after a press. While the pointer only hovers, those keys reach the terminal. No macOS menu item claims ⌘R.

## Tests

- Wire: `native/crates/attn-app/tests/wire/home_pull_requests.rs` (Home's Pull Requests card), `native/crates/attn-app/tests/wire/tile_header.rs` (the pull request on the tile header), `native/crates/attn-app/tests/wire/settings/hygiene.rs` (muted repositories and authors), `native/crates/attn-app/tests/wire/settings/agents.rs` (the own comments switch), `internal/daemon/agent_tools_pr_wire_test.go` (the pr tools), `internal/daemon/session_pull_request*_wire_test.go`, `internal/daemon/pr_*_wire_test.go` (`pr_host_discovery_wire_test.go` for several hosts), `internal/daemon/github_polling_wire_test.go`, `internal/daemon/gh_warning_wire_test.go`. Shared with [automations.md](automations.md): `internal/daemon/automation_review_demand_wire_test.go`, `internal/daemon/automation_review_origin_wire_test.go`.
- Stack: `cmd/attnd/pr_stack_test.go`.
- Kernel: `internal/github/*_test.go`, `internal/prreadiness/*_test.go`, `cli/attn/src/hooks/detect.rs`.

## Gotchas

- The mock GitHub answers no GraphQL. A watch on the baseline always reads HEALTH `delayed`, and you cannot drive the watch's ready, failed-checks and feedback notices live. The wire tests cover them.
- `pr wait-ready` runs the user's own `gh` against real GitHub and never reaches the mock. On the baseline you can drive only its usage refusals.
- A daemon started without the mock exports on a named instance polls nothing: the home screen says "GitHub polling is off for instance <name>".
- Record URLs on a host attn has a client for. With the mock that is `mock.github.local`. attn records a `github.com` URL but never refreshes it.
- The mock agent runs no harness hook, so drive `gh pr create` detection by calling `./attn _hook-tool-use <terminal>` with the tool call on stdin, as Claude Code's hook would.
- `attn pr` verbs other than `wait-ready` need a session. From a plain shell, pass `--session <id>`.
- ⌘R and `wsctl refresh-prs` poll the GitHub list only. A session's recorded pull request refreshes on its own 30-second tick.
- "New worktree session" reuses a folder in the Projects Directory only when one of its remotes is the pull request's repository, `https://<host>/<owner>/<repo>.git` or its SSH form. Any other repository by that name fails with "Failed to open PR: <dir>/<repo> holds a different repository: none of its remotes is https://<host>/<owner>/<repo>.git. Move it aside or change the Projects Directory". For a local clone, `git remote set-url origin https://<host>/<owner>/<repo>.git` and `git config url.<local repository>.insteadOf https://<host>/<owner>/<repo>.git` in the clone, so the fetch reaches the local repository.
- "New worktree session" needs a Projects Directory that holds a clone the launcher can fetch. The mock serves no Git, so with no clone the corner reads "Create failed: <repo>#<number> failed", and pressing it opens the reason. Set `projects_directory` back to its earlier value afterwards. `~/projects` is the default.
- Under `xvfb-run`, the window counts as covered, so a scroll lands only at the next view commit: a `bridge press` on a row below Home's Pull Requests card fold waits for it and times out. Seed the mock with `replace:true` and few enough pull requests that every row shows.
- A New worktree session onto a clone whose checkout holds the pull request's branch fails at `git worktree add`, which says the branch already exists. Give the clone another branch.
- The mock GitHub keeps a merged pull request open. The merged row leaves the list for this run of the app, and comes back after a relaunch.
- Approve and Merge on the baseline reach only the mock, so prove them through its `/__control` `requests`, not through GitHub. Their "Approved" and "Merged" names show for 1.5 seconds, so start the `bridge wait` right after the press.
- Working but wrong: a muted pull request has no way back. Settings lists muted repositories and authors, never single pull requests.
