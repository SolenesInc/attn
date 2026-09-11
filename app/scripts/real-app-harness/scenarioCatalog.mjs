export const scenarioCatalog = [
  {
    id: 'prompt-composition',
    runnerId: 'PromptComposition',
    label: 'Prompt delivery: ordinary/chief channels, peer attribution, crew wake/sleep and successor',
    command: ['node', 'scripts/real-app-harness/scenario-prompt-composition.mjs'],
    timeoutMs: 240_000,
  },
  {
    id: 'workspace-creation-shortcuts',
    runnerId: 'WORKSPACE-CREATION-SHORTCUTS',
    label: 'Workspace creation shortcuts',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-creation-shortcuts'],
  },
  {
    id: 'linux-shortcuts',
    runnerId: 'LINUX-SHORTCUTS',
    label: 'Linux terminal-style shortcuts through xdotool',
    command: ['pnpm', 'run', 'real-app:scenario-linux-shortcuts'],
    soakOnly: true,
  },
  {
    id: 'workspace-switching',
    runnerId: 'WORKSPACE-SWITCHING',
    label: 'Workspace switching',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-switching'],
  },
  {
    id: 'workspace-close-one-session-keeps-selection',
    runnerId: 'WORKSPACE-CLOSE-ONE-SESSION-KEEPS-SELECTION',
    label: 'Workspace close one session keeps selection',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-one-session-keeps-selection'],
  },
  {
    id: 'close-pane-nonblocking',
    runnerId: 'CLOSE-PANE-NONBLOCKING',
    label: 'Close pane does not wait for process teardown',
    command: ['pnpm', 'run', 'real-app:scenario-close-pane-nonblocking'],
  },
  {
    id: 'agent-close',
    runnerId: 'AgentClose',
    label: 'Agent close: a dispatcher closes its delegate, a sibling is refused, the seed keeps its tender',
    command: ['pnpm', 'run', 'real-app:scenario-agent-close'],
    timeoutMs: 240_000,
  },
  {
    id: 'session-close-ledger',
    runnerId: 'SESSION-CLOSE-LEDGER',
    label: 'Closing a worktree session records it and leaves the worktree alone',
    command: ['pnpm', 'run', 'real-app:scenario-session-close-ledger'],
  },
  {
    id: 'session-reopen',
    runnerId: 'SESSION-REOPEN',
    label: 'A closed worktree session reopens under its own id, recreating a deleted worktree only when asked',
    command: ['pnpm', 'run', 'real-app:scenario-session-reopen'],
  },
  {
    id: 'sessions-surface',
    runnerId: 'SESSIONS-SURFACE',
    label: 'The Sessions surface lists, filters, remembers its filters, and updates live and closed sessions',
    command: ['pnpm', 'run', 'real-app:scenario-sessions-surface'],
  },
  {
    id: 'tile-only-workspace-select',
    runnerId: 'TILE-ONLY-WORKSPACE-SELECT',
    label: 'Tile-only workspace select + render',
    command: ['pnpm', 'run', 'real-app:scenario-tile-only-workspace-select'],
  },
  {
    id: 'markdown-opener',
    runnerId: 'MARKDOWN-OPENER',
    label: 'Global Cmd+P markdown opener (git-enumerated fuzzy search + recents)',
    command: ['pnpm', 'run', 'real-app:scenario-markdown-opener'],
  },
  {
    id: 'notebook-editor-undo',
    runnerId: 'NOTEBOOK-EDITOR-UNDO',
    label: 'Notebook editor undo/redo (native Cmd+Z / Shift+Cmd+Z reach CodeMirror)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-editor-undo'],
  },
  {
    id: 'autoclose-on-exit',
    runnerId: 'AUTOCLOSE-ON-EXIT',
    label: 'Auto-close on clean exit, keep failed exits',
    command: ['pnpm', 'run', 'real-app:scenario-autoclose-on-exit'],
  },
  {
    id: 'garden-plot-dispatch',
    runnerId: 'GardenPlotDispatch',
    label: 'Garden plot dispatch: a plot is planted, a delegate is dispatched at it, and the panel walks it draining',
    command: ['pnpm', 'run', 'real-app:scenario-garden-plot-dispatch'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-tile-navigation',
    runnerId: 'GardenSeedTileNavigation',
    label: 'Garden seed tile navigation: a plot walks in, a native Escape unwinds it, Reveal hands the place to the Garden',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-tile-navigation'],
  },
  {
    id: 'garden-seed-read-receipts',
    runnerId: 'GardenSeedReadReceipts',
    label: 'Garden seed read receipts: inbox reads rearm generic doorbells without prompt-submit hooks',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-read-receipts'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-reopen',
    runnerId: 'GardenSeedReopen',
    label: 'Garden continuation: a closed tender resumes exactly, then hands the same seed to a new agent',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-reopen'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-header',
    runnerId: 'GardenSeedHeader',
    label: 'Garden header: five lifecycle states, latest note, outcome, and seed navigation',
    command: ['node', 'scripts/real-app-harness/scenario-garden-seed-header.mjs'],
    timeoutMs: 180_000,
  },
  {
    id: 'crew-seed-header',
    runnerId: 'CrewSeedHeader',
    label: 'Crew header: member claim, latest note and a fresh wake session',
    command: ['node', 'scripts/real-app-harness/scenario-crew-seed-header.mjs'],
    timeoutMs: 180_000,
  },
  {
    id: 'agent-settings',
    runnerId: 'AgentSettings',
    label: 'Agent settings: grouped defaults, background agents, autosave acknowledgements and close recovery',
    command: ['node', 'scripts/real-app-harness/scenario-agent-settings.mjs'],
  },
  {
    id: 'delegation-preferences',
    runnerId: 'DelegationPreferences',
    label: 'Delegation preferences: opt in, configure roles, override one launch, disable, and restore',
    command: ['pnpm', 'run', 'real-app:scenario-delegation-preferences'],
    timeoutMs: 300_000,
  },
  {
    id: 'countdown-cancel',
    runnerId: 'COUNTDOWN-CANCEL',
    label: 'Countdown cancel: a real Cmd+. stops the auto-settle and nudge countdowns on screen',
    command: ['pnpm', 'run', 'real-app:scenario-countdown-cancel'],
    timeoutMs: 300_000,
  },
  {
    id: 'agent-queue',
    runnerId: 'AGENT-QUEUE',
    label: 'Agent queue: a turn opens on a state, and closes only when the user settles or snoozes it',
    command: ['pnpm', 'run', 'real-app:scenario-agent-queue'],
    timeoutMs: 400_000,
  },
  {
    id: 'automation-lifecycle',
    runnerId: 'AUTOMATION-LIFECYCLE',
    label: 'Automation lifecycle: edit-rebind, delete-resurrect, cleanup-dirty-safe',
    command: ['pnpm', 'run', 'real-app:scenario-automation-lifecycle'],
    timeoutMs: 600_000,
  },
  {
    id: 'automation-form',
    runnerId: 'AUTOMATION-FORM',
    label: 'Automation form: validation, create, edit, collision, schedule and persistence',
    command: ['pnpm', 'run', 'real-app:scenario-automation-form'],
    timeoutMs: 240_000,
  },
  {
    id: 'automation-surface',
    runnerId: 'AUTOMATION-SURFACE',
    label: 'Automation panel: manual delivery, scheduled rows, disabled rejection and restart persistence',
    command: ['node', 'scripts/real-app-harness/scenario-automation-surface.mjs'],
    timeoutMs: 240_000,
  },
  {
    id: 'automation-scheduled-cleanup',
    runnerId: 'AUTOMATION-SCHEDULED-CLEANUP',
    label: 'Scheduled cleanup: restart catch-up, singleton coalescing, dirty worktree safety and storm guard',
    command: ['pnpm', 'run', 'real-app:scenario-automation-scheduled-cleanup'],
    timeoutMs: 240_000,
  },
  {
    id: 'worktree-surface',
    runnerId: 'WORKTREE-SURFACE',
    label: 'Worktrees panel: one refresh completes every verdict, the keep pin goes both ways, and a removal lands on its seed',
    command: ['pnpm', 'run', 'real-app:scenario-worktree-surface'],
    timeoutMs: 900_000,
  },
  {
    id: 'terminal-block-copy',
    skipOn: { linux: 'Cmd+C reaches the terminal through the macOS menu accelerator' },
    runnerId: 'TERMINAL-BLOCK-COPY',
    label: 'OSC 133 block copy via real fish + native Cmd+C',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-copy'],
  },
  {
    id: 'terminal-annotations',
    runnerId: 'TERMINAL-ANNOTATIONS',
    label: 'Annotate a live claude turn; it survives the next turn and an app relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-annotations'],
    timeoutMs: 600_000,
  },
  {
    id: 'terminal-context-menu',
    runnerId: 'TERMINAL-CONTEXT-MENU',
    label: 'Terminal context menu via native right-click + clipboard',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-context-menu'],
  },
  {
    id: 'terminal-input',
    runnerId: 'TERMINAL-INPUT',
    label: 'Terminal input and diagnostic report via packaged browser events, shortcuts, paste, and zoomed grid',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-input'],
  },
  {
    id: 'pty-host-setting',
    runnerId: 'PTY-HOST-SETTING',
    label: 'Experimental shared PTY opt-in preserves existing terminals',
    command: ['node', 'scripts/real-app-harness/scenario-pty-host-setting.mjs'],
  },
  {
    id: 'terminal-md-link',
    runnerId: 'TERMINAL-MD-LINK',
    label: 'Markdown path Cmd+click docks a session-bound markdown tile',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-md-link'],
  },
  {
    id: 'session-usage',
    runnerId: 'SESSION-USAGE',
    label: 'Session usage combines native subagents, keeps partial costs, and opens from the Action menu',
    command: ['pnpm', 'run', 'real-app:scenario-session-usage'],
  },
  {
    id: 'terminal-block-resize',
    runnerId: 'BLOCK-RESIZE',
    label: 'Block geometry through relaunch replay + split/close-split',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-resize'],
    timeoutMs: 360_000,
  },
  {
    id: 'tr205-probe-codex',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (codex vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:codex'],
    freshWorldAfter: true,
  },
  {
    id: 'tr205-probe-claude',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (claude vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:claude'],
    freshWorldAfter: true,
  },
  {
    id: 'tr502',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-502',
    label: 'TR-502 remote relaunch splits',
    command: ['pnpm', 'run', 'real-app:scenario-tr502'],
    freshWorldAfter: true,
  },
  {
    id: 'tr504',
    skipOn: { linux: { reason: 'needs a provisioned SSH machine; set ATTN_HARNESS_REMOTE_SSH_TARGET to its target to run it', unlessEnv: 'ATTN_HARNESS_REMOTE_SSH_TARGET' } },
    runnerId: 'TR-504',
    label: 'TR-504 remote cleanup',
    command: ['pnpm', 'run', 'real-app:scenario-tr504'],
    freshWorldAfter: true,
  },
  {
    id: 'tr201-local-claude',
    runnerId: 'TR-201',
    label: 'TR-201 relaunch restores an existing split with its content, SGR styling and deep colored scrollback',
    command: ['pnpm', 'run', 'real-app:scenario-tr201'],
  },
  {
    id: 'tr301-local-claude',
    runnerId: 'TR-301',
    label: 'TR-301 local claude utility focus',
    command: ['pnpm', 'run', 'real-app:scenario-tr301'],
  },
  {
    id: 'tr401-local-claude',
    runnerId: 'TR-401',
    label: 'TR-401 one window, three phases: Codex header frame, split-close redraw, split resize — codex and claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr401'],
  },
  {
    id: 'crash-recovery',
    runnerId: 'CRASH-REC',
    label: 'A machine crash keeps every session it can bring back and reaps the rest',
    command: ['pnpm', 'run', 'real-app:scenario-crash-recovery'],
    timeoutMs: 360_000,
  },
  {
    id: 'ghostty-scroll',
    runnerId: 'GHOSTTY-SCROLLBACK-ANCHOR',
    label: 'Ghostty scrollback anchoring while output streams',
    command: ['pnpm', 'run', 'real-app:scenario-ghostty-scroll'],
  },
  {
    id: 'present-submit-closes-window',
    runnerId: 'PRESENT-SUBMIT-CLOSES-WINDOW',
    label: 'Present: a waiting CLI opens it, the chip opens the real window, submitting from the window hides it and the CLI returns',
    command: ['pnpm', 'run', 'real-app:scenario-present-submit-closes-window'],
    timeoutMs: 240_000,
  },
  {
    id: 'automode-environment',
    runnerId: 'AutoModeEnvironment',
    label: 'Auto mode: a slot written from the pane and from the CLI, and what an unfilled one says',
    command: ['pnpm', 'run', 'real-app:scenario-automode-environment'],
    timeoutMs: 300_000,
  },
  {
    id: 'focus-probe',
    runnerId: 'FOCUS-PROBE',
    label: 'Focus probe (no focus steal on background session create)',
    command: ['pnpm', 'run', 'real-app:focus-probe'],
    // Not part of the serial matrix sweep — only runnable directly (run-soak).
    soakOnly: true,
  },
  {
    id: 'offset-soak',
    runnerId: 'OFFSET-SOAK',
    label: 'Seeded pane offset and overflow soak',
    command: ['pnpm', 'run', 'real-app:scenario-offset-soak'],
    soakOnly: true,
  },
  {
    id: 'perf-baseline',
    runnerId: 'perf-baseline',
    label: 'App CPU, memory, startup and streaming baseline',
    command: ['pnpm', 'run', 'real-app:scenario-perf-baseline'],
    soakOnly: true,
  },
  {
    id: 'perf-cold-warm',
    runnerId: 'perf-cold-warm',
    label: 'Cold and warm app performance comparison',
    command: ['pnpm', 'run', 'real-app:scenario-perf-cold-warm'],
    soakOnly: true,
  },
];

export function resolveScenarios(selected, catalog = scenarioCatalog) {
  const matrixCatalog = catalog.filter((scenario) => !scenario.soakOnly);
  if (!selected.length) {
    return matrixCatalog;
  }
  const byId = new Map(matrixCatalog.map((scenario) => [scenario.id, scenario]));
  return selected.map((id) => {
    const scenario = byId.get(id);
    if (!scenario) {
      throw new Error(`Unknown scenario id: ${id}`);
    }
    return scenario;
  });
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
