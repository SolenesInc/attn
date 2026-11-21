# In-app browser

The in-app browser is a tile that the user and their agents open and drive. An
agent opens a page beside itself, reads the page as a structured snapshot,
clicks and types, and captures screenshots or PDFs. The user watches the same
tile, navigates it, and signs in by hand when a site asks. Each desktop has at
most one browser tile, and the tile's cookies and storage outlive restarts.

## Sub-features

- `browser-open`: open a URL in the desktop's browser tile, beside the caller. Opening again navigates the same tile.
- `browser-navigate`: the address field, back, forward and reload.
- `browser-zoom`: zoom the focused page in, out and back to actual size.
- `browser-snapshot`: the page's URL, title, text and interactive elements, each with a reference to act on.
- `browser-locate`: find or wait for an element by CSS, XPath, link text, role and name, text, label, placeholder, alt, title or test id.
- `browser-input`: click, type into a field, press a key, scroll.
- `browser-capture`: save a screenshot or a PDF of the page to a private file.
- `browser-cookies`: list the page's cookies. Sign-ins persist.
- `browser-command`: call the WebDriver-shaped API directly.

## How to get to it

### People

- No key or palette command opens a browser tile. An agent or the user's shell does.
- In the tile (`native/crates/attn-app/src/browser/view.rs`): the "Browser address" field in place of the title (Enter navigates), "Reload browser", the pop-out button, "Close tile", and the page itself, clicked and typed into like any browser. On macOS the page is a `WKWebView` over the tile's page area (`native/crates/attn-app/src/browser/mac.rs`). While the page holds the keyboard, ⌘R reloads it, ⌘[ and ⌘] go back and forward, and ⌘=, ⌘- and ⌘0 zoom it (`native/crates/attn-app/src/keymap.rs`).
- ⌘W (`session.close`) closes the browser tile while the page or its address field has focus. The File menu item "Close Pane" runs the same action (`native/crates/attn-app/src/lib.rs`).
- The tile's row in the sidebar (`native/crates/attn-app/src/sidebar/mod.rs`): "Reload <title>" and "Close <title>" buttons, which take presses while the pointer is over the row.
- Where people see what agents do: the tile shows the page the agent drives as it changes, beside the agent's tile. The user types credentials themselves. Agents are told not to.
- Their own shell: the same `attn browser` verbs agents use, below. Without a session, the verbs act on the current desktop of the profile used most recently.

### Agents

- `attn browser` (the catalog's `internal/catalog/content/browser.yaml`, presented by `cli/attn/src/commands/browser/mod.rs`, run by `internal/daemon/commands_browser.go`): `open <url>`, `snapshot`, `find --using <strategy> --value <value> [--name <name>] [--all]`, `wait --using <strategy> --value <value> [--state attached|visible|hidden|detached] [--timeout <ms>]`, `click --selector <css>|--element <id>`, `type --selector <css>|--element <id> --text <text>`, `press --text <key>`, `scroll [--x <px>] [--y <px>]`, `reload`, `back`, `forward`, `cookies`, `command <action> [--params <json>]`, `screenshot [path]`, `pdf [path] [--params <json>]`. Each verb takes `--session <id>`.
- Tools, with `agent_tools_enabled` on: every verb but `wait` as `browser_<verb>` over `/mcp`. `browser_wait` is not offered, because it holds the call up to 120 s (D9); a tool caller looks with `browser_snapshot` or `browser_find`. A relative `browser_screenshot` or `browser_pdf` path resolves against the folder the session started in, and no path writes attn-browser.png or attn-browser.pdf there. The daemon writes captures, mode 0600, and prints the absolute path.
- Prompts: `attn skill --reference browser` covers the workflow, sessions, credentials, and treating page content as untrusted (`internal/prompts/content/skills/attn/references/browser.md`). The skill's capability index names the reference (`internal/prompts/content/skills/attn/SKILL.md`).

## Driving it

On macOS the app runs each browser tile's page in a `WKWebView` over the tile's page area, `tile.tile-browser.page`. The header holds `tile.tile-browser.address`, a field named "Browser address" in place of the title, and `tile.tile-browser.reload`, named "Reload browser". The page is not in the accessibility tree and `bridge shot` leaves it out: read it with `attn browser snapshot` and see it in a VM recording. It hides while the palette, a dialog, a menu, a popover, a callout or a drag shows. A click in the page gives it the keyboard and makes its tile the active one.

While the page holds the keyboard, the keys split as a browser splits them, by action, so a rebound key follows its action. attn keeps the keys that move around attn: ⌘W, ⌘Q, ⌘N, ⌘T, ⌘1–⌘9, ⌘J, ⇧⌘J, ⌘G, the ⌘⌥ arrows, ⇧⌘O, every leader chord and every other key attn binds. The page keeps the keys that act on what it shows: ⌘F, ⌘B, ⌘K, ⌘D, ⌘↑, ⌘↓, ⌘↵ and the edit keys ⌘A, ⌘C, ⌘X, ⌘V, ⌘Z and ⇧⌘Z. When the page does not use a key it keeps, nothing happens. ⌘R reloads the page, ⌘[ and ⌘] go back and forward in its history, and ⌘=, ⌘- and ⌘0 zoom it in, out and back to actual size.

On Linux the page area says "Browser tiles are not available in this app on this platform yet.", the app does not offer itself as the browser host, and every verb but `open` fails with `no in-app browser host is connected`. The tile's sidebar row, `sidebar.tile.<desktop>.tile-browser`, holds `sidebar.tile.<desktop>.tile-browser.reload`, named "Reload <title>", and `sidebar.tile.<desktop>.tile-browser.close`, named "Close <title>".

A browser tile popped out into its own window shows its page there, with its address field and Reload in the window's header, and the agent verbs act on it as on the desktop. The page stays loaded while its tile is out, whichever desktop is current. Bringing the tile back moves the same page to its desktop, with its history, without loading it again.

Preconditions:

- On macOS, in the VM ([macOS VM](../../../../docs/internal/macos-vm.md)), use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says. The app is the browser host. Without the app, every verb but `open` fails.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-browser.XXXX) && echo "export P=$P" >> $DRIVE_ENV`. Save `page.html` in it as `<title>Drive page</title><h1>Drive heading</h1><input aria-label="Search"><button onclick="document.querySelector('h1').textContent='Clicked '+document.querySelector('input').value">Go</button><a href="other.html">Other</a>`, and `other.html` as `<title>Other page</title><h1>Other heading</h1>`. The headings differ from the titles so a text wait matches the page, not its `<title>`.
- Start a local page server with `{ python3 -m http.server 8765 --bind 127.0.0.1 --directory $P & echo "export SRV=$!" >> $DRIVE_ENV; }`. Stop it at the end with `kill $SRV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-browser-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

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

- Also save `find.html` in `$P` as `<title>Find page</title><h1>Find heading</h1><input aria-label="Search"><div id="find" hidden>Find box</div><a href="other.html">Other</a><script>addEventListener('keydown',e=>{if(e.metaKey&&e.key==='f'){e.preventDefault();document.getElementById('find').hidden=false}})</script>`, and keep a second agent waiting and a second desktop for the **Keys** step.
- Start the agent on the current desktop with `wsctl add-session --cwd $A --agent claude --label web-agent --id web-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **An agent opens a page.** Run `wsctl input --id <agent> --text "web-open" --enter`. `./attn agent peek web-agent` shows `opened http://127.0.0.1:8765/page.html`. In `wsctl list`, the current desktop's `tree_json` holds a tile whose `tile_kind` is `browser` and whose `tile_params` is that URL, and `bridge wait '{"target":{"key":"tile.tile-browser.page"}}'` answers.
- **Snapshot.** Run `wsctl input --id <agent> --text "web-snapshot" --enter`. The turn waits for the page's heading, then peek shows JSON with `"title": "Drive page"` and `elements` for the input, the button and the link, each with an `attn-element-` reference.
- **Find, type, click and wait.** Run `wsctl input --id <agent> --text "web-find" --enter`: peek shows the button's reference, such as `{"element-6066-11e4-a52e-4f735466cecf":"attn-element-<n>"}`. Run `wsctl input --id <agent> --text "web-type" --enter`, then `wsctl input --id <agent> --text "web-click attn-element-<n>" --enter`. Peek shows no error, and a new snapshot reads `Clicked drive` in its `text`.
- **Keys and scroll.** Run `wsctl input --id <agent> --text "web-keys" --enter`. Peek shows both commands answered without error.
- **History.** Run `wsctl input --id <agent> --text "web-history" --enter`. The link click, back, forward and reload each wait for the page they land on. Peek ends with `Other page` from `get_title`, and `wsctl list` shows `tile_params` on `other.html`.
- **Cookies.** Run `wsctl input --id <agent> --text "web-cookies" --enter`: peek shows the cookie list, empty for this page.
- **Captures.** Run `wsctl input --id <agent> --text "web-capture" --enter`. Peek shows two absolute paths ending in `/shot.png` and `/page.pdf`. `file $A/shot.png` prints PNG, `file $A/page.pdf` prints PDF, and `stat -f %Lp $A/shot.png` prints `600`.
- **The person navigates.** Run `bridge press '{"target":{"key":"tile.tile-browser.address"}}'`, press `mod+a`, type `127.0.0.1:8765/page.html` and press `Enter`. The field reads `http://127.0.0.1:8765/page.html`, `wsctl list` shows `tile_params` there, and `web-snapshot` shows `"title": "Drive page"`. Press `tile.tile-browser.reload`, then hover the sidebar row `sidebar.tile.<desktop>.tile-browser` and press its `sidebar.tile.<desktop>.tile-browser.reload`: each reloads the page, and the page stays where it is.
- **The person's shell.** Run `./attn browser open http://127.0.0.1:8765/other.html`. It prints `opened http://127.0.0.1:8765/other.html` and exits 0, the same tile navigates to `other.html`, and `wsctl list` shows `tile_params` there. `./attn browser snapshot --session web-agent` prints the snapshot, titled `Other page`, from the instance shell.
- **Keys.** Open the page with `./attn browser open http://127.0.0.1:8765/find.html --session web-agent`. In the VM, activate the app and click the page. Click the link: `Other heading` shows. Press ⌘[: `Find heading` shows. Press ⌘]: `Other heading` shows. Press ⌘[ again, type `abc` into Search and press ⌘R: Search is empty. Press ⌘F: `Find box` shows. Press ⌘J: the waiting agent's tile becomes the active one. Click the page again and press ⌘1 from the other desktop: desktop 1 shows.
- **Close with ⌘W.** In the VM recording, click the page, then press ⌘W: the browser tile closes and the agent's tile stays. With the address field focused, ⌘W closes the browser tile too. A further agent `web-snapshot` fails with an error that ends `browser_control: no browser tile is open for that session`.
- **Close from the sidebar.** Open the page again with `./attn browser open http://127.0.0.1:8765/page.html --session web-agent`. Run `bridge hover '{"target":{"key":"sidebar.tile.<desktop>.tile-browser"}}'` and `bridge press '{"target":{"key":"sidebar.tile.<desktop>.tile-browser.close"}}'`: the tile leaves `wsctl list`.
- **Popped out.** Open the page again and pop the browser tile out with its header's pop-out button, `tile.tile-browser.popout`: in the VM recording the page shows in the tile window. Send `web-snapshot`: peek shows the page's snapshot. Click a link in the page, then press ⌘[ in the tile window: the page goes back. Close the tile window: the tile returns to its desktop showing the same page, and ⌘] goes forward again, so its history came with it.
- **Under overlays.** Open the palette over the tile, then a callout (`./attn callout show --session web-agent desktops "Here"`): the page hides under each and comes back when it closes, in the VM recording.
- **Refusals.** `./attn browser open file:///etc/hosts` exits 1 with `open_browser: only http and https urls are supported`. `./attn browser find` exits 1 with `browser find: --using and --value are required` and the usage line.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek web-agent` after each agent step, `wsctl list` after the open, the person's navigation and the close, the screenshot and PDF the agent saved, and the VM recording of the page, the focus handoff and the close. Name each file for its way in, such as `browser-agent-snapshot-peek.txt`.

### In the Tauri app (the rollback)

Install and launch it as [../SKILL.md](../SKILL.md#the-tauri-app-the-rollback) says, then make `$P`, the page server, `$A` and `web-agent` as above. The agent verbs, the person's shell and the refusals drive as above. `bridge dom_*` reads see the tile's header and address field but not the page, and `bridge capture_screenshot` leaves the page out, so capture it with `bridge capture_native_window_screenshot`, which needs macOS Screen Recording permission for the instance's app or your terminal. The tile's DOM can remain mounted while offscreen, so a DOM box alone does not prove a native click can reach it. Show the tile's desktop before native navigation or page-focus checks.

- **An agent opens a page.** After `web-open`, `bridge dom_wait '{"selector":"[data-tile-kind=\"browser\"]","timeoutMs":5000}'` matches.
- **The person navigates.** Select `web-agent` for setup with `bridge select_session '{"sessionId":"web-agent"}'` and check that `bridge get_state` shows `view: "session"`. Resolve the point of `[data-tile-kind="browser"] [aria-label="Browser address"]` with `bridge dom_native_point`, click it with `bridge native_mouse`, then press ⌘A with `bridge native_key '{"key":"a","modifiers":["command"]}'` so the typed address replaces the old one. A native click does not leave the old address selected, so without ⌘A the typed address goes into the old one. Run `bridge native_text '{"text":"http://127.0.0.1:8765/page.html"}'` and `bridge native_key '{"key":"Enter"}'`. `wsctl list` shows `tile_params` on `page.html`. Click `[data-tile-kind="browser"] [aria-label="Reload browser"]` natively: the page reloads and stays on `page.html`.
- **Close with ⌘W.** Resolve the center of `[data-tile-kind="browser"] .browser-tile-host`, the page area, with `bridge dom_native_point` and click it natively: `bridge get_browser_focus_state` shows a `label`. Run `bridge native_key '{"key":"w","modifiers":["command"]}'`: `bridge dom_wait '{"selector":"[data-tile-kind=\"browser\"]","absent":true,"timeoutMs":5000}'` matches and the agent's tile is still there.
- **From the sidebar.** Open the page again. Move the pointer over the tile's sidebar row, `[data-testid^="sidebar-tile-"]` (`bridge native_mouse` with `"action":"move"`), then click `[data-testid^="reload-tile-"]` natively: the page reloads and `tile_params` stays. Click `[data-testid^="close-tile-"]` the same way: the tile leaves `wsctl list`.
- **Proof.** Capture the page with `bridge capture_native_window_screenshot '{"path":"<P>/tauri-browser.png"}'`, `<P>` written out.

## Contexts

- **Durability across restarts**: the browser tile and its last URL come back with the desktop after any restart, and cookies and local storage persist, so a sign-in survives. The app keeps session cookies too, in the cookie jar `~/Library/Application Support/<bundle id>/browser/cookies.json`, and puts them back before a page loads. The jar follows a sign-out the page does in place, so a relaunch does not sign the user back in. A request's change reaches the jar at once, and so does a script's change on a secure page, which has `cookieStore`. On a plain `http` page a script's change reaches the jar with the page's next request, load or address change. The app also saves the jar as it quits and waits for the write, so a sign-out just before Quit holds too. Persistent cookies live in the WebKit data store, which the Tauri app (the rollback) and the native app of one instance share, so the native app's first launch keeps the Tauri app's sign-ins, session ones included.
- **Linux**: `attn browser` works the same where the daemon and CLI run on Linux, but the page runs in the app: a daemon on Linux drives a browser only through an app connected to it.
- **Outposts**: the browser tile lives on a home desktop and the page loads in the app on the user's machine, so `localhost` in a URL is that machine, not the outpost an agent runs on.
- **Profiles**: the tile opens on a desktop of the caller's profile, and `--session` targets a session's desktop in its own profile.
- **The shelf**: an off-screen browser tile keeps its native view, hidden, so `attn browser` verbs work and the screen stays as it is. Its page survives the trip back on screen, with no reload.
- **Performance and idle cost**: closing a browser tile frees its web view, so its WebContent process exits. A hidden or covered browser tile hides its native view. A callout over the tile hides the native view until the callout ends ([callouts.md](callouts.md)). Nothing measures an idle browser tile's cost.
- **Harnesses**: every harness drives the browser with the same `attn browser` verbs and reads the same reference.
- **Keyboard and native input**: the page is a native view, and while it holds the keyboard the keys split by action as Driving it says. The keys bound to history back and forward, ⌘[ and ⌘], go back and forward in the page, and the keys bound to the app's text size, ⌘=, ⌘- and ⌘0, zoom the page instead. A rebound key follows its action. In the Tauri app (the rollback), once a click puts focus in the page, keys go to the page, and attn's own shortcuts reach attn again after a click on attn's chrome. There ⌘W closes the tile through the File menu, zoom moves in Safari's steps from 50% to 300%, and ⌘Z and ⇧⌘Z undo and redo in the page's text fields through the browser runtime.

## Tests

- Wire: `native/crates/attn-app/tests/wire/browser.rs`, `internal/daemon/browser_wire_test.go`, `internal/daemon/browser_test.go`, `internal/daemon/agent_tools_browser_wire_test.go` (the tools).
- Stack: `cmd/attnd/agent_commands_stack_test.go`.
- Scenario: `native-browser`, `native-browser-session-cookies`, and in the Tauri app (the rollback) `browser-page-keys`, `browser-wait-navigation`, `callouts`.
- Kernel: `native/crates/attn-app/tests/page_keys.rs`, and the Tauri app's (the rollback) Rust tests in `app/src-tauri/core/src/browser_host.rs` and `app/src-tauri/core/src/browser_alerts.rs`.
- App: the Tauri app's (the rollback) `app/src/browser/runtime.test.ts`, and `app/src/App.shelfLayout.test.tsx` for an off-screen tile.

## Gotchas

- An agent opening and driving the browser does not take the user off Home. Show the tile's desktop before navigation or page-focus checks.
- Mock browser JSON is one long line, and the terminal can wrap inside a title or an element reference. Read the logical JSON across screen rows instead of waiting for an unbroken fragment.
- With only the daemon, `attn browser open` places the tile and exits 0, and every other verb fails with `browser_control: no in-app browser host is connected`. Launch the app first.
- attn loads a desktop's browser tile only while that desktop is the current or the previous one, or while the tile is popped out into its own window. Verbs on any other desktop fail after 5 seconds with `the browser tile on desktop <id> is not loaded`, until the user opens that desktop.
- Verbs before `open` fail with `no browser tile is open for that session`. Each desktop holds one browser tile. A second `open` navigates that tile instead of adding one.
- Element references come from one page load. After navigation or reload, find the element again. A read-only `wait` keeps checking the requested condition across navigation within its original timeout. The `wait` does not reuse an element reference from the old document.
- A verb that follows `open` or a navigation can reach a page that has not loaded. Wait on something the new page shows (`attn browser wait --using css --value h1 --state visible`) before reading the page. A text wait checks the first element with that text, which can be the page's `<title>`, so wait on text that only the body holds.
- Going back can restore the page as it was left, edits included. Wait on an element that tells the pages apart, not on text the page changes itself.
- `screenshot` and `pdf` write to the folder `attn` runs in, `attn-browser.png` or `attn-browser.pdf` by default, and replace a file of that name.
- A recipe that points at a public site depends on the network, so serve local pages instead.
- Working but wrong: no key, palette command or sidebar control opens a browser tile, so a person without a shell has no way in, and once a person closes the tile in the app, they cannot open it again from the app.
