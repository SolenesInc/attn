# In-app browser

A browser tile that the user and their agents open and drive. An agent opens
a page beside itself, reads it as a structured snapshot, clicks and types, and
captures screenshots or PDFs; the user watches the same tile, navigates it,
and signs in by hand when a site asks. Each desktop has at most one browser
tile, and its cookies and storage outlive restarts.

## Sub-features

- `browser-open`: open a URL in the desktop's browser tile, beside the caller; opening again navigates the same tile.
- `browser-navigate`: the address field, back, forward and reload.
- `browser-snapshot`: the page's URL, title, text and interactive elements, each with a reference to act on.
- `browser-locate`: find or wait for an element by CSS, XPath, link text, role and name, text, label, placeholder, alt, title or test id.
- `browser-input`: click, type into a field, press a key, scroll.
- `browser-capture`: save a screenshot or a PDF of the page to a private file.
- `browser-cookies`: list the page's cookies; sign-ins persist.
- `browser-command`: call the WebDriver-shaped API directly.

## How to get to it

### People

- No key or palette command opens a browser tile; an agent or the user's shell does.
- In the tile (`app/src/components/SessionTerminalDesktop/DesktopDockTile.tsx`, `BrowserAddressForm.tsx`, `BrowserTileBody.tsx`): the "Browser address" field (Enter navigates), "Reload browser", "Close tile", and the page itself, clicked and typed into like any browser.
- File → "Close Pane" (⌘W) closes the browser tile while the page has focus (`app/src-tauri/core/src/lib.rs`).
- The tile's row in the sidebar (`app/src/components/SidebarRows.tsx`): "Reload <title>" and "Close <title>" buttons, which take clicks while the pointer is over the row.
- Where people see what agents do: the tile shows the page the agent drives as it changes, beside the agent's tile. The user types credentials themselves; agents are told not to.
- Their own shell: the same `attn browser` verbs agents use, below. Without a session they act on the current desktop of the profile used most recently.

### Agents

- `attn browser` (`cmd/attn/main.go`): `open <url>`, `snapshot`, `find --using <strategy> --value <value> [--name <name>] [--all]`, `wait --using <strategy> --value <value> [--state attached|visible|hidden|detached] [--timeout <ms>]`, `click --selector <css>|--element <id>`, `type --selector <css>|--element <id> --text <text>`, `press --text <key>`, `scroll [--x <px>] [--y <px>]`, `reload`, `back`, `forward`, `cookies`, `command <action> [--params <json>]`, `screenshot [path]`, `pdf [path] [--params <json>]`. Each takes `--session <id>`.
- Prompts: the attn skill's browser reference, on the workflow, sessions, credentials and page content as untrusted (`internal/prompts/content/skills/attn/references/browser.md`), named in its capability index (`internal/prompts/content/skills/attn/SKILL.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The app is the browser host; without it every verb but `open` fails.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-browser.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, holding `page.html`: `<title>Drive page</title><h1>Drive heading</h1><input aria-label="Search"><button onclick="document.querySelector('h1').textContent='Clicked '+document.querySelector('input').value">Go</button><a href="other.html">Other</a>`, and `other.html`: `<title>Other page</title><h1>Other heading</h1>`. The headings differ from the titles so a text wait matches the page, not its `<title>`.
- A local page server: `{ python3 -m http.server 8765 --bind 127.0.0.1 --directory $P & echo "export SRV=$!" >> $DRIVE_ENV; }`. Stop it at the end with `kill $SRV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-browser-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "web-open", "actions": [
    {"type": "attn", "args": ["browser", "open", "http://127.0.0.1:8765/page.html"]}]},
  {"includes": "web-snapshot", "actions": [
    {"type": "attn", "args": ["browser", "wait", "--using", "css", "--value", "h1", "--state", "visible"]},
    {"type": "attn", "args": ["browser", "snapshot"]}]},
  {"includes": "web-find", "actions": [
    {"type": "attn", "args": ["browser", "find", "--using", "role", "--value", "button", "--name", "Go"]}]},
  {"includes": "web-type", "actions": [
    {"type": "attn", "args": ["browser", "type", "--selector", "input", "--text", "drive"]}]},
  {"includes": "web-click", "actions": [
    {"type": "capture", "name": "el", "pattern": "(attn-element-[0-9]+)"},
    {"type": "attn", "args": ["browser", "click", "--element", "{{el}}"]},
    {"type": "attn", "args": ["browser", "wait", "--using", "text", "--value", "Clicked drive", "--state", "visible"]}]},
  {"includes": "web-keys", "actions": [
    {"type": "attn", "args": ["browser", "press", "--text", "Tab"]},
    {"type": "attn", "args": ["browser", "scroll", "--y", "200"]}]},
  {"includes": "web-history", "actions": [
    {"type": "attn", "args": ["browser", "click", "--selector", "a"]},
    {"type": "attn", "args": ["browser", "wait", "--using", "text", "--value", "Other heading", "--state", "visible"]},
    {"type": "attn", "args": ["browser", "back"]},
    {"type": "attn", "args": ["browser", "wait", "--using", "css", "--value", "input", "--state", "visible"]},
    {"type": "attn", "args": ["browser", "forward"]},
    {"type": "attn", "args": ["browser", "wait", "--using", "text", "--value", "Other heading", "--state", "visible"]},
    {"type": "attn", "args": ["browser", "reload"]},
    {"type": "attn", "args": ["browser", "command", "get_title"]}]},
  {"includes": "web-cookies", "actions": [
    {"type": "attn", "args": ["browser", "cookies"]}]},
  {"includes": "web-capture", "actions": [
    {"type": "attn", "args": ["browser", "screenshot", "shot.png"]},
    {"type": "attn", "args": ["browser", "pdf", "page.pdf"]}]}
]}
```

- The agent on the current desktop: `wsctl add-session --cwd $A --agent claude --label web-agent --id web-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **An agent opens a page.** Run `wsctl input --id <agent> --text "web-open" --enter`. `./attn agent peek web-agent` shows `opened http://127.0.0.1:8765/page.html`. In `wsctl list`, the current desktop's `tree_json` holds a tile whose `tile_kind` is `browser` and whose `tile_params` is that URL, and `bridge dom_wait '{"selector":"[data-tile-kind=\"browser\"]","timeoutMs":5000}'` matches.
- **Snapshot.** Run `wsctl input --id <agent> --text "web-snapshot" --enter`. The turn waits for the page's heading, then peek shows JSON with `"title": "Drive page"` and `elements` for the input, the button and the link, each with an `attn-element-` reference.
- **Find, type, click and wait.** Run `wsctl input --id <agent> --text "web-find" --enter`: peek shows the button's reference, such as `{"element-6066-11e4-a52e-4f735466cecf":"attn-element-<n>"}`. Run `wsctl input --id <agent> --text "web-type" --enter`, then `wsctl input --id <agent> --text "web-click attn-element-<n>" --enter`. Peek shows no error, and a new snapshot reads `Clicked drive` in its `text`.
- **Keys and scroll.** Run `wsctl input --id <agent> --text "web-keys" --enter`. Peek shows both commands answered without error.
- **History.** Run `wsctl input --id <agent> --text "web-history" --enter`. The link click, back, forward and reload each wait for the page they land on. Peek ends with `Other page` from `get_title`, and `wsctl list` shows `tile_params` on `other.html`.
- **Cookies.** Run `wsctl input --id <agent> --text "web-cookies" --enter`: peek shows the cookie list, empty for this page.
- **Captures.** Run `wsctl input --id <agent> --text "web-capture" --enter`. Peek shows two absolute paths ending in `/shot.png` and `/page.pdf`; `file $A/shot.png` says PNG, `file $A/page.pdf` says PDF, and `stat -f %Lp $A/shot.png` prints `600`.
- **The person navigates.** Read the box of `[data-tile-kind="browser"] [aria-label="Browser address"]` with `bridge dom_bounds`, convert it as the README says, click it with `bridge native_mouse`, then run `bridge native_key '{"key":"a","modifiers":["command"]}'`, `bridge native_text '{"text":"http://127.0.0.1:8765/page.html"}'` and `bridge native_key '{"key":"Enter"}'`. Select all explicitly: a click in an already focused address field places the cursor, so typing alone can splice the new URL into the old one. `wsctl list` shows `tile_params` on `page.html`, and `wsctl input --id <agent> --text "web-snapshot" --enter` shows `"title": "Drive page"`. Click `[data-tile-kind="browser"] [aria-label="Reload browser"]` natively: the page reloads and stays on `page.html`.
- **The person's shell.** Run `./attn browser open http://127.0.0.1:8765/other.html`. It prints `opened http://127.0.0.1:8765/other.html` and exits 0, the same tile navigates to `other.html`, and `wsctl list` shows `tile_params` there. `./attn browser snapshot --session web-agent` prints the snapshot, titled `Other page`, from the instance shell.
- **Close with ⌘W.** Take the box of `[data-tile-kind="browser"] .browser-tile-host`, the page area, with `bridge dom_bounds`, convert it as the README says, and click its center natively: `bridge get_browser_focus_state` shows a `label`. Run `bridge native_key '{"key":"w","modifiers":["command"]}'`: `bridge dom_wait '{"selector":"[data-tile-kind=\"browser\"]","absent":true,"timeoutMs":5000}'` matches and the agent's tile is still there. A further agent `web-snapshot` fails with an error that ends `browser_control: no browser tile is open for that session`.
- **From the sidebar.** Open the page again with `./attn browser open http://127.0.0.1:8765/page.html --session web-agent`. Move the pointer over the tile's sidebar row, `[data-testid^="sidebar-tile-"]` (`bridge native_mouse` with `"action":"move"`), click `[data-testid^="reload-tile-"]` natively: the page reloads and `tile_params` stays. This reload way in remains unverified through the bridge with its window parked as Gotchas describes. Click `[data-testid^="close-tile-"]` the same way: the tile leaves `wsctl list`.
- **Refusals.** `./attn browser open file:///etc/hosts` exits 1 with `open_browser: only http and https urls are supported`. `./attn browser find` exits 1 with `attn browser find: --using and --value are required`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek web-agent` after each agent step, `wsctl list` after the open, the person's navigation and the close, and the screenshot and PDF the agent saved. Capture the page with `bridge capture_native_window_screenshot '{"path":"<P>/browser-native.png"}'`, `<P>` written out, since the page is a native view the web view capture leaves out. Put the way in in each file's name, such as `browser-agent-snapshot-peek.txt`.

## Contexts

- **Durability across restarts**: the browser tile and its last URL come back with the desktop after any restart, and cookies and local storage persist, so a sign-in survives.
- **Linux**: `attn browser` works the same where the daemon and CLI run on Linux, but the page runs in the app: a daemon on Linux drives a browser only through an app connected to it.
- **Outposts**: the browser tile lives on a home desktop and the page loads in the app on the user's machine, so `localhost` in a URL is that machine, not the outpost an agent runs on.
- **Profiles**: the tile opens on a desktop of the caller's profile, and `--session` targets a session's desktop in its own profile.
- **Performance and idle cost**: a hidden or covered browser tile hides its native view; a callout over it hides it until the callout ends ([callouts.md](callouts.md)). Nothing measures an idle browser tile's cost.
- **Harnesses**: every harness drives the browser with the same `attn browser` verbs and reads the same reference.
- **Keyboard and native input**: the page is a native view. Once a click puts focus in it, keys go to the page, and attn's own shortcuts reach attn again after a click on attn's chrome. ⌘W still closes the tile through the File menu.

## Tests

- Wire: `internal/daemon/browser_wire_test.go`, `internal/daemon/browser_test.go`.
- Stack: `cmd/attn/agent_commands_stack_test.go`.
- Scenario: `callouts`.
- Kernel: the Rust tests in `app/src-tauri/core/src/browser_host.rs` and `app/src-tauri/core/src/browser_alerts.rs`.
- App: `app/src/browser/runtime.test.ts`.

## Gotchas

- With only the daemon, `attn browser open` places the tile and exits 0, and every other verb fails with `browser_control: no in-app browser host is connected`. Launch the app first.
- Verbs before `open` fail with `no browser tile is open for that session`. Each desktop holds one browser tile; a second `open` navigates it instead of adding one.
- `bridge dom_*` reads see the tile's header and address field but not the page; `bridge capture_screenshot` leaves the page out. Read the page with `attn browser snapshot` and capture it with `bridge capture_native_window_screenshot`.
- Element references come from one page load. After navigation or reload, find the element again.
- A verb that follows `open` or a navigation can reach a page that has not loaded. Wait on something the new page shows (`attn browser wait --using css --value h1 --state visible`) before reading it. A text wait checks the first element with that text, which can be the page's `<title>`, so wait on text that only the body holds.
- Going back can restore the page as it was left, edits included. Wait on an element that tells the pages apart, not on text the page changes itself.
- `screenshot` and `pdf` write to the folder `attn` runs in, `attn-browser.png` or `attn-browser.pdf` by default, and replace a file of that name.
- A recipe that points at a public site depends on the network; serve local pages instead.
- `bridge capture_native_window_screenshot` needs macOS Screen Recording permission for the instance's app or your terminal; without it the capture fails.
- Working but wrong: no key, palette command or sidebar control opens a browser tile, so a person without a shell has no way in, and closing the tile is a one-way door from the app.
- A native sidebar reload click produced no new page request in the local server log during the live drive. The subsequent sidebar close did remove the tile. Keep reload unverified; close proof does not prove reload. Native `move` alone did not put the sidebar row in CSS `:hover`.
- A supplementary reopen followed by `attn browser wait --using css --value h1 --state visible --session web-agent` failed with `browser_control: timed out waiting for browser runtime action`, although the local server received the page request. The reload check could not continue from that state.
