export const scenarioCatalog = [
  {
    id: 'native-sidebar-session',
    runnerId: 'NATIVE-SIDEBAR-SESSION',
    label: 'The native app launches parked, lists a shell session in its sidebar, takes a press on its row and paints the session menu row that keys reach',
    command: ['node', 'scripts/real-app-harness/scenario-native-sidebar-session.mjs'],
  },
  {
    id: 'native-terminal-core',
    runnerId: 'NATIVE-TERMINAL-CORE',
    label: 'A shell in the native app draws what is typed, and a program that tracks the mouse hears presses, releases and the wheel',
    command: ['node', 'scripts/real-app-harness/scenario-native-terminal-core.mjs'],
  },
  {
    id: 'native-desktop-tiles',
    runnerId: 'NATIVE-DESKTOP-TILES',
    label: 'Two shells side by side in the native app: a divider drag saves their widths, zoom, focus alone and back, and tile history',
    command: ['node', 'scripts/real-app-harness/scenario-native-desktop-tiles.mjs'],
  },
  {
    id: 'native-chief-slot',
    runnerId: 'NATIVE-CHIEF-SLOT',
    label: "A fresh profile's Chief setup in the native app fits the window, takes the Notebook folder and starts the Chief from the keyboard alone, and nothing marks a session chief",
    command: ['node', 'scripts/real-app-harness/scenario-native-chief-slot.mjs'],
  },
  {
    id: 'native-codex-shared',
    runnerId: 'NATIVE-CODEX-SHARED',
    label: 'In the native app a shared Codex terminal moves between conversations in one tile; the session it leaves stays hidden in the queue and ledger, a hidden approval is answered from the queue and after /resume, and closing the last tile closes it',
    command: ['node', 'scripts/real-app-harness/scenario-native-codex-shared.mjs'],
    freshWorldAfter: true,
  },
  {
    id: 'native-codex-update',
    runnerId: 'NATIVE-CODEX-UPDATE',
    label: "The native app's Codex updates band offers a newer Codex, installs it in a tile, and reloads the tile still on the older Codex",
    command: ['node', 'scripts/real-app-harness/scenario-native-codex-update.mjs'],
    freshWorldAfter: true,
  },
  {
    id: 'native-crew-management',
    runnerId: 'NATIVE-CREW-MANAGEMENT',
    label: "Manage crew in the native app: members' seeds open as readers and Back returns, the member menu saves and clears launch settings, the full charter saves, recovers and meets an outside edit, a letter's seed link opens, a save lost with the daemon goes once it returns, and Close then Start carries the saved settings",
    command: ['node', 'scripts/real-app-harness/scenario-native-crew-management.mjs'],
    timeoutMs: 300_000,
    freshWorldAfter: true,
  },
  {
    id: 'native-crew-seed-header',
    runnerId: 'NATIVE-CREW-SEED-HEADER',
    label: "In the native app a crew member's tile header names the seeds it claims, keeps the claim into its next session, and opens the seed",
    command: ['node', 'scripts/real-app-harness/scenario-native-crew-seed-header.mjs'],
    timeoutMs: 180_000,
  },
  {
    id: 'native-delegation-chain',
    runnerId: 'NATIVE-DELEGATION-CHAIN',
    label: 'In the native app delegates carry their roles into the header and the delegation chain, opened from a row, the header and the palette; a delegate on standby stays in it as a dormant row that opens its seed; a delegate to a missing numbered desktop leaves the caller on screen',
    command: ['node', 'scripts/real-app-harness/scenario-native-delegation-chain.mjs'],
  },
  {
    id: 'native-analytics-settings',
    runnerId: 'NATIVE-ANALYTICS-SETTINGS',
    label: "The native app's Analytics settings: vents new then done, a tool vent kept here, dismiss and undo by key, plant a seed, records by stream with app actions, and clear the local record",
    command: ['node', 'scripts/real-app-harness/scenario-native-analytics-settings.mjs'],
  },
  {
    id: 'native-delegation-preferences',
    runnerId: 'NATIVE-DELEGATION-PREFERENCES',
    label: "The native app's Delegation settings: opt in, an instance refusing the workflow install, a custom role's model, effort and alternative, delete and undo, one launch's effort override, and off and on again",
    command: ['node', 'scripts/real-app-harness/scenario-native-delegation-preferences.mjs'],
  },
  {
    id: 'native-move-with-delegates',
    runnerId: 'NATIVE-MOVE-WITH-DELEGATES',
    label: 'The native palette moves a dispatcher, two delegates and a nested delegate to another desktop together',
    command: ['node', 'scripts/real-app-harness/scenario-native-move-with-delegates.mjs'],
  },
  {
    id: 'native-clear-opens-session',
    runnerId: 'NATIVE-CLEAR-OPENS-SESSION',
    label: 'In the native app /clear opens a new session in the same tile and sidebar row, typing goes on, and the ledger lists the cleared one closed',
    command: ['node', 'scripts/real-app-harness/scenario-native-clear-opens-session.mjs'],
  },
  {
    id: 'native-conversations-surface',
    runnerId: 'NATIVE-CONVERSATIONS-SURFACE',
    label: 'The native Conversations list shows why each copy is kept, keeps and unkeeps from the row, and forgets after a confirmation that names crew letters',
    command: ['node', 'scripts/real-app-harness/scenario-native-conversations-surface.mjs'],
  },
  {
    id: 'native-session-close-ledger',
    runnerId: 'NATIVE-SESSION-CLOSE-LEDGER',
    label: 'Closing a worktree session in the native app keeps the worktree, asks nothing, and the ledger reads it closed across a daemon restart',
    command: ['node', 'scripts/real-app-harness/scenario-native-session-close-ledger.mjs'],
  },
  {
    id: 'native-session-reopen',
    runnerId: 'NATIVE-SESSION-REOPEN',
    label: 'The native ledger reopens a closed worktree session under its own id, and recreates a deleted worktree only when its offered action is pressed',
    command: ['node', 'scripts/real-app-harness/scenario-native-session-reopen.mjs'],
  },
  {
    id: 'native-sessions-surface',
    runnerId: 'NATIVE-SESSIONS-SURFACE',
    label: 'The native Sessions list filters by repository, profile, scope and dates, updates a row in place on close, reopens on the filters it was left with, and keeps a refused reopen in the ledger',
    command: ['node', 'scripts/real-app-harness/scenario-native-sessions-surface.mjs'],
  },
  {
    id: 'native-callouts',
    runnerId: 'NATIVE-CALLOUTS',
    label: 'Native callouts step through the queue sidebar, reveal and go to an agent, fall back off screen, point in desktop flow, return after the palette, and leave typing with the terminal',
    command: ['node', 'scripts/real-app-harness/scenario-native-callouts.mjs'],
  },
  {
    id: 'native-desktop-order',
    runnerId: 'NATIVE-DESKTOP-ORDER',
    label: 'Sort desktops from the native palette orders numbered desktops then names, and its Undo puts back the order before',
    command: ['node', 'scripts/real-app-harness/scenario-native-desktop-order.mjs'],
  },
  {
    id: 'native-browser',
    runnerId: 'NATIVE-BROWSER',
    label: 'An agent drives a browser tile in the native app: open, wait, type, click, a dialog, history, captures, cookies, and the close key',
    command: ['node', 'scripts/real-app-harness/scenario-native-browser.mjs'],
    skipOn: { linux: 'The native app runs browser pages in WKWebView, which only macOS has; the Linux tile says browser tiles are unavailable' },
  },
  {
    id: 'native-browser-session-cookies',
    runnerId: 'NATIVE-BROWSER-SESSION-COOKIES',
    label: "A browser tile's session cookies outlive the native app: the Tauri app's session sign-in goes with the first page after cut-over, a native session cookie comes back after a relaunch, and one the page deletes in place stays deleted, even just before Quit",
    command: ['node', 'scripts/real-app-harness/scenario-native-browser-session-cookies.mjs'],
    skipOn: { linux: 'The native app runs browser pages in WKWebView, which only macOS has; the Linux tile says browser tiles are unavailable' },
  },
  {
    id: 'native-tile-windows',
    runnerId: 'NATIVE-TILE-WINDOWS',
    label: 'A document tile in the native app pops out into its own window, which hides when the tile comes back and shows again for the next pop-out',
    command: ['node', 'scripts/real-app-harness/scenario-native-tile-windows.mjs'],
  },
  {
    id: 'native-desktop-switching',
    runnerId: 'NATIVE-DESKTOP-SWITCHING',
    label: 'Desktop keys in the native app switch, move a tile following or staying, make a desktop on an empty digit and bounce; the overview switches, the app follows another client, and a document tile takes the keyboard',
    command: ['node', 'scripts/real-app-harness/scenario-native-desktop-switching.mjs'],
  },
  {
    id: 'native-row-overview',
    runnerId: 'NATIVE-ROW-OVERVIEW',
    label: "The native overview draws a row's terminals small, walks them, previews another desktop and comes back, draws a terminal never shown from its snapshot and one with none as a card, and lands, with no terminal resized",
    command: ['node', 'scripts/real-app-harness/scenario-native-row-overview.mjs'],
  },
  {
    id: 'native-close-desktop',
    runnerId: 'NATIVE-CLOSE-DESKTOP',
    label: 'The native close key closes an empty desktop with its launcher focused and shows the desktop above, and a desktop holding a shell closes from its sidebar row only through a confirmation',
    command: ['node', 'scripts/real-app-harness/scenario-native-close-desktop.mjs'],
  },
  {
    id: 'native-shelf-peek',
    runnerId: 'NATIVE-SHELF-PEEK',
    label: 'Native app shelf peek: an entry shows a live terminal, a reply reaches it at its size, Enter opens the tile, an entry leaving the screen under a still pointer opens nothing, a stack peeks from its pill and its dots, and a stopped terminal keeps the reply',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-peek.mjs'],
  },
  {
    id: 'native-title-bar',
    runnerId: 'NATIVE-TITLE-BAR',
    label: 'Native app title bar: clicks reach every control, an entry opens its peek, a tile drops beside an entry, and tiles start below the band',
    command: ['node', 'scripts/real-app-harness/scenario-native-title-bar.mjs'],
  },
  {
    id: 'native-desktop-splits',
    runnerId: 'NATIVE-DESKTOP-SPLITS',
    label: 'Split shells on two native desktops keep to their desktop across switches, a closed split leaves the others and their scrollback, and focus shows one alone and back',
    command: ['node', 'scripts/real-app-harness/scenario-native-desktop-splits.mjs'],
  },
  {
    id: 'native-close-pane',
    runnerId: 'NATIVE-CLOSE-PANE',
    label: 'Closing a native split keeps the remaining shell, the most recently used tile takes over, a desktop emptied by a close bounces back, and an agent ignoring SIGTERM leaves at once despite slow Git',
    command: ['node', 'scripts/real-app-harness/scenario-native-close-pane.mjs'],
  },
  {
    id: 'native-shelf-drags',
    runnerId: 'NATIVE-SHELF-DRAGS',
    label: 'Native app shelf drags: entries onto tiles, between entries and onto a sidebar desktop, a tile beside an entry, column dividers past the narrowest and after a window resize, and stack entries onto a tile edge',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-drags.mjs'],
  },
  {
    id: 'native-shelf-layout',
    runnerId: 'NATIVE-SHELF-LAYOUT',
    label: 'Native app: a row of shells past the screen comes back after a window sweep, the row keys slide to each end with the sidebar open and hidden, the width keys and control step the tile count, and the right key past the row end goes to the next desktop',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-layout.mjs'],
  },
  {
    id: 'native-shelf-motion',
    runnerId: 'NATIVE-SHELF-MOTION',
    label: 'Native app: each slide along a row of shells and a closing tile come to rest with nothing left leaving and only the screen\'s tiles drawn',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-motion.mjs'],
  },
  {
    id: 'native-shelf-moves',
    runnerId: 'NATIVE-SHELF-MOVES',
    label: 'Native app: a split at capacity keeps its anchor, close keeps focus in the slot, carry and send to the shelf, documents agents open wait off screen for the jump key, and entries and the sidebar go to a tile without reordering the row',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-moves.mjs'],
  },
  {
    id: 'native-shelf-drop-beside',
    runnerId: 'NATIVE-SHELF-DROP-BESIDE',
    label: 'Native app: a tile dropped beside an entry comes on screen next to it, active, for every drop place on a four-tile row that shows two and when a saved width keeps the pair apart',
    command: ['node', 'scripts/real-app-harness/scenario-native-shelf-drop-beside.mjs'],
  },
  {
    id: 'native-terminal-features',
    runnerId: 'NATIVE-TERMINAL-FEATURES',
    label: 'A shell in the native app scrolls, selects and copies, selects a command block, finds through its history and draws kitty images placed at a cell and by placeholders',
    command: ['node', 'scripts/real-app-harness/scenario-native-terminal-features.mjs'],
  },
  {
    id: 'native-terminal-annotations',
    runnerId: 'NATIVE-TERMINAL-ANNOTATIONS',
    label: 'An Alt-drag in the native app marks an agent message; a label, a comment and a note go to the agent with the send key',
    command: ['node', 'scripts/real-app-harness/scenario-native-terminal-annotations.mjs'],
  },
  {
    id: 'native-markdown-annotations',
    runnerId: 'NATIVE-MARKDOWN-ANNOTATIONS',
    label: 'A selection in a native markdown tile, in a paragraph or a list item, takes a quick label or a comment, and Send delivers them to the bound agent',
    command: ['node', 'scripts/real-app-harness/scenario-native-markdown-annotations.mjs'],
  },
  {
    id: 'native-terminal-md-link',
    runnerId: 'NATIVE-TERMINAL-MD-LINK',
    label: 'A markdown path Cmd-clicked in a native terminal docks a tile bound to its agent, and again reuses it',
    command: ['node', 'scripts/real-app-harness/scenario-native-terminal-md-link.mjs'],
  },
  {
    id: 'native-terminal-labels',
    runnerId: 'NATIVE-TERMINAL-LABELS',
    label: 'Holding Cmd+L (Ctrl+Shift+L on Linux) labels the ids in a native terminal, a label opens its card with the keyboard, letting go hides them, and no key reaches the shell',
    command: ['node', 'scripts/real-app-harness/scenario-native-terminal-labels.mjs'],
  },
  {
    id: 'native-markdown-opener',
    runnerId: 'NATIVE-MARKDOWN-OPENER',
    label: 'The native markdown opener docks tracked and untracked files by a fuzzy query, and a recent reuses its tile',
    command: ['node', 'scripts/real-app-harness/scenario-native-markdown-opener.mjs'],
  },
  {
    id: 'native-notebook-editor-undo',
    runnerId: 'NATIVE-NOTEBOOK-EDITOR-UNDO',
    label: 'An editor tile beside a shell opens its finder and a note that saves itself, and undo and redo reach the editor and the file, not the terminal',
    command: ['node', 'scripts/real-app-harness/scenario-native-notebook-editor-undo.mjs'],
  },
  {
    id: 'native-notebook-link-nav',
    runnerId: 'NATIVE-NOTEBOOK-LINK-NAV',
    label: 'An editor tile follows links on a modifier press: to a sibling note, into a folder, to a heading, and up to a note whose image draws',
    command: ['node', 'scripts/real-app-harness/scenario-native-notebook-link-nav.mjs'],
  },
  {
    id: 'native-notebook-tile-close',
    runnerId: 'NATIVE-NOTEBOOK-TILE-CLOSE',
    label: 'The close key with an editor tile active closes the tile and keeps the shell beside it',
    command: ['node', 'scripts/real-app-harness/scenario-native-notebook-tile-close.mjs'],
  },
  {
    id: 'native-garden-seed-header',
    runnerId: 'NATIVE-GARDEN-SEED-HEADER',
    label: 'A delegate’s header names its seed through all five states, its card follows the latest note and the outcome, and the chip opens the seed beside the agent',
    command: ['node', 'scripts/real-app-harness/scenario-native-garden-seed-header.mjs'],
  },
  {
    id: 'native-garden-walk',
    runnerId: 'NATIVE-GARDEN-WALK',
    label: 'The garden finds a plot, walks it in the dock and in columns, keeps the walk through Escape, walks it again in a seed tile, and shows only the selected profile’s seeds',
    command: ['node', 'scripts/real-app-harness/scenario-native-garden-walk.mjs'],
  },
  {
    id: 'native-garden-seed-authorization',
    runnerId: 'NATIVE-GARDEN-SEED-AUTHORIZATION',
    label: 'The user sets and clears a tended seed’s authorization in its seed tile with Shift+Enter, Enter and Escape, and the tending agent is typed each change',
    command: ['node', 'scripts/real-app-harness/scenario-native-garden-seed-authorization.mjs'],
  },
  {
    id: 'native-garden-seed-reopen',
    runnerId: 'NATIVE-GARDEN-SEED-REOPEN',
    label: 'A closed delegate resumes from its seed in the garden and reports to it again, then Handover gives the same seed to a new agent with the user’s note',
    command: ['node', 'scripts/real-app-harness/scenario-native-garden-seed-reopen.mjs'],
  },
  {
    id: 'native-settings',
    runnerId: 'NATIVE-SETTINGS',
    label: 'The native Settings dialog opens three ways, searches, saves a click and a field, shows a refusal and a shell change, and closes three ways',
    command: ['node', 'scripts/real-app-harness/scenario-native-settings.mjs'],
  },
  {
    id: 'native-agent-settings',
    runnerId: 'NATIVE-AGENT-SETTINGS',
    label: 'Native agent settings: switches, the default agent, a harness executable, a refused then saved context cap, a price override and the background cap',
    command: ['node', 'scripts/real-app-harness/scenario-native-agent-settings.mjs'],
  },
  {
    id: 'native-self-update',
    runnerId: 'NATIVE-SELF-UPDATE',
    label: 'Native self-update: Settings and the palette each update a named instance, and its band follows the run to the relaunched app',
    command: ['node', 'scripts/real-app-harness/scenario-native-self-update.mjs'],
    freshWorldAfter: true,
    skipOn: { darwin: 'Patches the daemon binary in place, which breaks its code signature on macOS' },
  },
  {
    id: 'native-shortcut-editor',
    runnerId: 'NATIVE-SHORTCUT-EDITOR',
    label: 'Native shortcut editor: physical keys open the cheat sheet, record a rebind and a chord that then run, ask before taking keys, and reset and restore',
    command: ['node', 'scripts/real-app-harness/scenario-native-shortcut-editor.mjs'],
    skipOn: { darwin: 'The recorder hears physical keys, which only xdotool sends; the automation server sends logical keys' },
  },
  {
    id: 'native-quick-capture',
    runnerId: 'NATIVE-QUICK-CAPTURE',
    label: 'Native Quick Capture: system-wide shortcut, keys without activating attn, paste and drop, delivery, Recent and a draft across a restart',
    command: ['node', 'scripts/real-app-harness/scenario-native-quick-capture.mjs'],
    skipOn: { linux: 'Quick Capture uses a macOS nonactivating panel' },
    timeoutMs: 600_000,
  },
  {
    id: 'native-profile-lifecycle',
    runnerId: 'NATIVE-PROFILE-LIFECYCLE',
    label: 'Native profiles are created, renamed and deleted from the switcher, an agent keeps its profile, and the last profile stays',
    command: ['node', 'scripts/real-app-harness/scenario-native-profile-lifecycle.mjs'],
  },
  {
    id: 'native-session-usage',
    runnerId: 'NATIVE-SESSION-USAGE',
    label: 'A native tile header shows usage with native subagents and partial pricing, previews it on hover and pins it from the palette',
    command: ['node', 'scripts/real-app-harness/scenario-native-session-usage.mjs'],
  },
  {
    id: 'native-agent-queue',
    runnerId: 'NATIVE-AGENT-QUEUE',
    label: 'The native queue lists turns oldest first, opens a row, hands over on settle and snooze, lands on home and takes a woken agent back',
    command: ['node', 'scripts/real-app-harness/scenario-native-agent-queue.mjs'],
  },
  {
    id: 'native-palette-order',
    runnerId: 'NATIVE-PALETTE-ORDER',
    label: 'The native palette finds and shows an agent, raises a picked command under the pinned ones, switches on >, and runs priority and rename',
    command: ['node', 'scripts/real-app-harness/scenario-native-palette-order.mjs'],
  },
  {
    id: 'native-session-creation',
    runnerId: 'NATIVE-SESSION-CREATION',
    label: 'The native launcher on an empty desktop and the picker on both new-session keys start terminals in typed folders',
    command: ['node', 'scripts/real-app-harness/scenario-native-session-creation.mjs'],
  },
  {
    id: 'native-notebook-sync',
    runnerId: 'NATIVE-NOTEBOOK-SYNC',
    label: 'Native Notebook sync on the outpost row: first sync, previous folder rescue, conflict copies and a sync error',
    command: ['node', 'scripts/real-app-harness/scenario-native-notebook-sync.mjs'],
  },
  {
    id: 'native-outpost-update',
    runnerId: 'NATIVE-OUTPOST-UPDATE',
    label: 'Native Outpost updates band and row: a failed Add offers the update, the band installs and counts down, a missing build offers the row',
    command: ['node', 'scripts/real-app-harness/scenario-native-outpost-update.mjs'],
  },
  {
    id: 'native-outposts-settings',
    runnerId: 'NATIVE-OUTPOSTS-SETTINGS',
    label: 'Native outposts: opt in, add and answer ssh, remote shell, split and agent, stalled, unreachable and connecting tiles, offline queue, Home and palette, Re-pair and Remove',
    command: ['node', 'scripts/real-app-harness/scenario-native-outposts-settings.mjs'],
  },
  {
    id: 'native-automation-form',
    runnerId: 'NATIVE-AUTOMATION-FORM',
    label: 'The native automation form validates before saving, gives new definitions an ID, recovers a stale revision, leaves the switch its own column, deletes on a second press, and shows a guarded schedule\'s checks in the drawer',
    command: ['node', 'scripts/real-app-harness/scenario-native-automation-form.mjs'],
    timeoutMs: 600_000,
  },
  {
    id: 'native-automation-surface',
    runnerId: 'NATIVE-AUTOMATION-SURFACE',
    label: 'The native automations drawer runs a definition at once, shows the run on its desktop, in the palette, the queue sidebar and the queue bar, says why a disabled run is refused, and keeps runs across a restart',
    command: ['node', 'scripts/real-app-harness/scenario-native-automation-surface.mjs'],
    timeoutMs: 240_000,
  },
];

export function resolveScenarios(selected, catalog = scenarioCatalog) {
  return selected.length ? selected.map((id) => resolveScenario(id, catalog)) : catalog;
}

export function scenariosAllowingRealAgents(catalog = scenarioCatalog) {
  return catalog.filter((scenario) => scenario.allowRealAgents !== undefined);
}

export function allowRealAgentsForRunner(runnerId, catalog = scenarioCatalog) {
  const entries = typeof runnerId === 'string' && runnerId
    ? catalog.filter((scenario) => scenario.runnerId === runnerId)
    : [];
  if (entries.length === 0) {
    throw new Error([
      `agent tripwire: runner ${JSON.stringify(runnerId)} has no scenarioCatalog.mjs entry, so nothing declares whether it may run a real agent.`,
      `declare it: add a catalog entry carrying runnerId ${JSON.stringify(runnerId)}, or pass allowRealAgents to createScenarioRunner —`,
      '  false to arm the tripwire, true for every binary, or an array naming the agent binaries the scenario needs.',
    ].join('\n'));
  }
  if (entries.some((scenario) => scenario.allowRealAgents === true)) {
    return true;
  }
  const named = entries.flatMap((scenario) => (Array.isArray(scenario.allowRealAgents) ? scenario.allowRealAgents : []));
  return named.length > 0 ? [...new Set(named)] : undefined;
}

export function resolveScenario(id, catalog = scenarioCatalog) {
  const scenario = catalog.find((entry) => entry.id === id);
  if (!scenario) {
    throw new Error(`Unknown scenario id: ${id}`);
  }
  return scenario;
}
