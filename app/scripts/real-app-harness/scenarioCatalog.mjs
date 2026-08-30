// runnerId is the scenarioId the entry's script hands createScenarioRunner (null
// when it has no runner), and the key allowRealAgents is looked up by.
export const scenarioCatalog = [
  {
    id: 'workspace-shell-lifecycle',
    runnerId: 'WORKSPACE-SHELL-LIFECYCLE',
    label: 'Workspace shell lifecycle',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-shell-lifecycle'],
  },
  {
    id: 'workspace-creation-shortcuts',
    runnerId: 'WORKSPACE-CREATION-SHORTCUTS',
    label: 'Workspace creation shortcuts',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-creation-shortcuts'],
  },
  {
    id: 'workspace-switching',
    runnerId: 'WORKSPACE-SWITCHING',
    label: 'Workspace switching',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-switching'],
  },
  {
    id: 'workspace-move-leaf',
    runnerId: 'WORKSPACE-MOVE-LEAF',
    label: 'Workspace move pane between workspaces',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-move-leaf'],
  },
  {
    id: 'workspace-close-last-session-switches-back',
    runnerId: 'WORKSPACE-CLOSE-LAST-SESSION-SWITCHES-BACK',
    label: 'Workspace close last session switches back',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-last-session-switches-back'],
  },
  {
    id: 'workspace-close-one-session-keeps-selection',
    runnerId: 'WORKSPACE-CLOSE-ONE-SESSION-KEEPS-SELECTION',
    label: 'Workspace close one session keeps selection',
    command: ['pnpm', 'run', 'real-app:scenario-workspace-close-one-session-keeps-selection'],
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
    id: 'notebook-tile-finder',
    runnerId: 'NOTEBOOK-TILE-FINDER',
    label: 'Notebook tile finder (native Cmd+Opt+N dock, Cmd+P re-summon)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-tile-finder'],
  },
  {
    id: 'notebook-editor-undo',
    runnerId: 'NOTEBOOK-EDITOR-UNDO',
    label: 'Notebook editor undo/redo (native Cmd+Z / Shift+Cmd+Z reach CodeMirror)',
    command: ['pnpm', 'run', 'real-app:scenario-notebook-editor-undo'],
  },
  {
    id: 'editor-workspace-root',
    runnerId: 'EDITOR-WORKSPACE-ROOT',
    label: 'Editor tile over an arbitrary workspace root (off-root gating + positive control)',
    command: ['pnpm', 'run', 'real-app:scenario-editor-workspace-root'],
  },
  {
    id: 'autoclose-on-exit',
    runnerId: 'AUTOCLOSE-ON-EXIT',
    label: 'Auto-close on clean exit, keep failed exits',
    command: ['pnpm', 'run', 'real-app:scenario-autoclose-on-exit'],
  },
  {
    id: 'present-flow',
    runnerId: 'PRESENT-FLOW',
    label: 'Present flow: waiting CLI → window → submit round → synchronous feedback',
    command: ['pnpm', 'run', 'real-app:scenario-present-flow'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-handoff',
    runnerId: 'GardenSeedHandoff',
    label: 'Garden seed handoff: one session leaves a handoff and ends, the next is primed on tend',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-handoff'],
  },
  {
    id: 'garden-plot-dispatch',
    runnerId: 'GardenPlotDispatch',
    label: 'Garden plot dispatch: a plot is planted, a delegate is dispatched at it, and the panel walks it draining',
    command: ['pnpm', 'run', 'real-app:scenario-garden-plot-dispatch'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-delegation-reporting',
    runnerId: 'GardenDelegationReporting',
    label: 'Garden delegation reporting: a delegation reports on its seed — log notes, artifacts, steering',
    command: ['pnpm', 'run', 'real-app:scenario-garden-delegation-reporting'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-nudges',
    runnerId: 'GardenSeedNudges',
    allowRealAgents: true,
    label: 'Garden seed nudges: a ringing note and harvest reach the dispatcher with a read reset between them',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-nudges'],
    timeoutMs: 240_000,
  },
  {
    id: 'garden-seed-reopen',
    runnerId: 'GardenSeedReopen',
    label: 'Garden surfaces: a delegated pane names its seed, and a closed tender is reopened from the drill',
    command: ['pnpm', 'run', 'real-app:scenario-garden-seed-reopen'],
    timeoutMs: 240_000,
  },
  {
    id: 'ordinary-delegation-ticket',
    runnerId: 'ORDINARY-DELEGATION-TICKET',
    allowRealAgents: true,
    label: 'Ordinary delegation: a non-chief session delegates, and the bound ticket routes to worker, delegator, and chief',
    command: ['pnpm', 'run', 'real-app:scenario-ordinary-delegation-ticket'],
    timeoutMs: 360_000,
  },
  {
    id: 'nudge-trigger',
    runnerId: 'NUDGE-TRIGGER',
    label: 'Ticket nudge: paused gate holds, then the real "deliver now" button doorbells the agent',
    command: ['pnpm', 'run', 'real-app:scenario-nudge-trigger'],
    timeoutMs: 360_000,
  },
  {
    id: 'countdown-cancel',
    runnerId: 'COUNTDOWN-CANCEL',
    allowRealAgents: true,
    label: 'Countdown cancel: a real Cmd+. stops the auto-settle and nudge countdowns on screen',
    command: ['pnpm', 'run', 'real-app:scenario-countdown-cancel'],
    timeoutMs: 420_000,
  },
  {
    id: 'settle-typing-hold',
    runnerId: 'SETTLE-TYPING-HOLD',
    allowRealAgents: true,
    label: 'Settle typing hold: typing to an agent freezes its settling countdown, and going quiet hands back a whole one',
    command: ['pnpm', 'run', 'real-app:scenario-settle-typing-hold'],
    timeoutMs: 300_000,
  },
  {
    id: 'agent-queue',
    runnerId: 'AGENT-QUEUE',
    allowRealAgents: true,
    label: 'Agent queue: a turn opens on a state and closes only when the user settles it',
    command: ['pnpm', 'run', 'real-app:scenario-agent-queue'],
    timeoutMs: 900_000,
  },
  {
    id: 'agent-queue-snooze',
    runnerId: 'AGENT-QUEUE-SNOOZE',
    allowRealAgents: true,
    label: 'Agent queue snooze: a deferral closes the turn, suppresses the next one, and wakes to the tail',
    command: ['pnpm', 'run', 'real-app:scenario-agent-queue-snooze'],
    timeoutMs: 900_000,
  },
  {
    id: 'automation-lifecycle',
    runnerId: 'AUTOMATION-LIFECYCLE',
    label: 'Automation lifecycle: edit-rebind, delete-resurrect, cleanup-dirty-safe',
    command: ['pnpm', 'run', 'real-app:scenario-automation-lifecycle'],
    timeoutMs: 600_000,
  },
  {
    id: 'terminal-block-copy',
    runnerId: 'TERMINAL-BLOCK-COPY',
    label: 'OSC 133 block copy via real fish + native Cmd+C',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-copy'],
  },
  {
    id: 'terminal-scrollback-colors',
    runnerId: 'TERMINAL-SCROLLBACK-COLORS',
    label: 'Terminal scrollback keeps indexed and truecolor cell colors',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-scrollback-colors'],
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
    label: 'Terminal input via packaged browser events, shortcuts, paste, and zoomed grid',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-input'],
  },
  {
    id: 'terminal-osc8-link',
    runnerId: 'TERMINAL-OSC8-LINK',
    label: 'OSC 8 hyperlink Cmd+click via native click + local HTTP probe',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-osc8-link'],
  },
  {
    id: 'terminal-md-link',
    runnerId: 'TERMINAL-MD-LINK',
    label: 'Markdown path Cmd+click docks a session-bound markdown tile',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-md-link'],
  },
  {
    id: 'terminal-seed-preview',
    runnerId: 'TERMINAL-SEED-PREVIEW',
    label: 'Known terminal seed ID hover preview and icon-only tile action',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-seed-preview'],
  },
  {
    id: 'terminal-block-resize',
    runnerId: 'BLOCK-RESIZE',
    label: 'Block geometry across fish/bash/zsh through relaunch replay + split/close-split',
    command: ['pnpm', 'run', 'real-app:scenario-terminal-block-resize'],
    timeoutMs: 360_000,
  },
  {
    id: 'tr205-probe-codex',
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (codex vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:codex'],
  },
  {
    id: 'tr205-probe-claude',
    runnerId: 'TR-205',
    label: 'TR-205 remote probe (claude vocabulary)',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'probe:claude'],
  },
  {
    id: 'tr502',
    runnerId: 'TR-502',
    label: 'TR-502 remote relaunch splits',
    command: ['pnpm', 'run', 'real-app:scenario-tr502'],
  },
  {
    id: 'tr504',
    runnerId: 'TR-504',
    label: 'TR-504 remote cleanup',
    command: ['pnpm', 'run', 'real-app:scenario-tr504'],
  },
  {
    id: 'tr402-local-codex',
    runnerId: 'TR-402',
    allowRealAgents: true,
    label: 'TR-402 local codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-codex'],
  },
  {
    id: 'tr402-local-claude',
    runnerId: 'TR-402',
    allowRealAgents: true,
    label: 'TR-402 local claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-claude'],
  },
  {
    id: 'tr201-local-claude',
    runnerId: 'TR-201',
    allowRealAgents: true,
    label: 'TR-201 local claude existing split relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-tr201'],
  },
  {
    id: 'tr204-local-claude',
    runnerId: 'TR-204',
    allowRealAgents: true,
    label: 'TR-204 local claude relaunch formatting',
    command: ['pnpm', 'run', 'real-app:scenario-tr204'],
  },
  {
    id: 'tr301-local-claude',
    runnerId: 'TR-301',
    allowRealAgents: true,
    label: 'TR-301 local claude utility focus',
    command: ['pnpm', 'run', 'real-app:scenario-tr301'],
  },
  {
    id: 'tr401-local-claude',
    runnerId: 'TR-401',
    allowRealAgents: true,
    label: 'TR-401 local claude resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401'],
  },
  {
    id: 'tr401-local-codex',
    runnerId: 'TR-401',
    allowRealAgents: true,
    label: 'TR-401 local codex resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-local-codex'],
  },
  {
    id: 'tr401-codex-initial-pane',
    runnerId: 'TR-401-CODEX-MAIN',
    allowRealAgents: true,
    label: 'TR-401 Codex fresh initial-pane resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401-codex-main'],
  },
  {
    id: 'codex-resume',
    runnerId: 'TR-CODEX-RESUME',
    allowRealAgents: true,
    label: 'Codex native resume id mapping',
    command: ['pnpm', 'run', 'real-app:scenario-codex-resume'],
  },
  {
    id: 'recoverable-auto-revive',
    runnerId: null,
    allowRealAgents: true,
    label: 'Recoverable Claude session auto-revives after daemon restart',
    command: ['pnpm', 'run', 'real-app:scenario-recoverable-auto-revive'],
    timeoutMs: 360_000,
  },
  {
    id: 'crash-recovery',
    runnerId: 'CRASH-REC',
    allowRealAgents: true,
    label: 'A machine crash keeps every session it can bring back and reaps the rest',
    command: ['pnpm', 'run', 'real-app:scenario-crash-recovery'],
    timeoutMs: 360_000,
  },
  {
    id: 'snapshot-scrollback-restore',
    runnerId: 'SNAPSHOT-SCROLLBACK-RESTORE',
    allowRealAgents: true,
    label: 'Deep scrollback survives an app relaunch restore',
    command: ['pnpm', 'run', 'real-app:scenario-snapshot-scrollback-restore'],
  },
  {
    id: 'ghostty-scroll',
    runnerId: 'GHOSTTY-SCROLLBACK-ANCHOR',
    allowRealAgents: true,
    label: 'Ghostty scrollback anchoring while output streams',
    command: ['pnpm', 'run', 'real-app:scenario-ghostty-scroll'],
  },
  {
    id: 'present-submit-closes-window',
    runnerId: 'PRESENT-SUBMIT-CLOSES-WINDOW',
    label: 'Present submit closes the real presentation window',
    command: ['pnpm', 'run', 'real-app:scenario-present-submit-closes-window'],
  },
  {
    id: 'nisse-conversation',
    runnerId: 'PI-HOST-CONVERSATION',
    allowRealAgents: ['pi'],
    label: 'Conversation session: nisse round trip, second prompt after settle, no orphans on close',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-conversation'],
    // Needs the attn-pi plugin installed in the target profile
    // (`attn plugin install-bundled attn-pi`) and pi credentials.
    timeoutMs: 300_000,
  },
  {
    id: 'nisse-nudge',
    runnerId: 'PI-HOST-NUDGE',
    allowRealAgents: ['pi'],
    label: 'Conversation session: steer mid-run, nudge an idle session, state and turn',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-nudge'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 420_000,
  },
  {
    id: 'nisse-tools',
    runnerId: 'PI-HOST-TOOLS',
    allowRealAgents: ['pi'],
    label: 'Conversation session: tool cards, on-demand detail, full output, patch as a diff, queue cancel',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-tools'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-revive',
    runnerId: 'PI-HOST-REVIVE',
    allowRealAgents: ['pi'],
    label: 'Conversation session: kill -9 to recoverable, reload with history, snapshot on a cold client',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-revive'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-delegate',
    runnerId: 'PI-HOST-DELEGATE',
    allowRealAgents: ['pi'],
    label: 'Delegation to a conversation agent: brief as the first message, the agent reports on its own ticket, a brief survives a crash before the first word',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-delegate'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 900_000,
  },
  {
    id: 'nisse-history',
    runnerId: 'PI-HOST-HISTORY',
    allowRealAgents: ['pi'],
    label: 'Conversation session: resume an existing conversation file, page a long transcript, switch model mid-session',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-history'],
    // Same prereqs as nisse-conversation.
    timeoutMs: 600_000,
  },
  {
    id: 'nisse-markdown-stream',
    runnerId: 'NISSE-MARKDOWN-STREAM',
    allowRealAgents: ['pi'],
    label: 'Conversation session: a recorded reply replayed into the pane renders as markdown while it streams',
    command: ['pnpm', 'run', 'real-app:scenario-nisse-markdown-stream'],
    // Needs attn-pi installed, like the other nisse scenarios, but calls no model:
    // the reply is a recording, so the run is deterministic and free.
    timeoutMs: 300_000,
  },
  {
    id: 'app-reconcile',
    runnerId: 'APP-RECONCILE',
    label: 'App reconcile: version move rebuilds, a real trim gap disables loudly, an interrupted rebuild repairs',
    command: ['pnpm', 'run', 'real-app:scenario-app-reconcile'],
    // Needs bun for `attn app apply`; the Linux witness leg self-skips when the
    // VM has no attn.
    timeoutMs: 600_000,
  },
  {
    id: 'pi-automode',
    runnerId: 'PI-AUTOMODE',
    allowRealAgents: ['pi'],
    label: 'pi auto mode: envelope invisibility, a denial and its surfaces, a conversational grant, a quiet session, the circuit breaker',
    command: ['pnpm', 'run', 'real-app:scenario-pi-automode'],
    // Needs `pi` on PATH and the attn-pi plugin installed, but no credentials and
    // no network: the model and the classifier are both a loopback stub.
    timeoutMs: 900_000,
  },
  {
    id: 'automode-environment',
    runnerId: 'AutoModeEnvironment',
    label: 'Auto mode environment: a slot written from the pane and from the CLI, and what an unfilled one says',
    command: ['pnpm', 'run', 'real-app:scenario-automode-environment'],
    timeoutMs: 300_000,
  },
  {
    id: 'automode-no-model',
    runnerId: 'AutoModeNoModel',
    label: 'Auto mode with no model: off until one is named, on when a proposal is promoted',
    command: ['pnpm', 'run', 'real-app:scenario-automode-no-model'],
    timeoutMs: 300_000,
  },
  {
    id: 'focus-probe',
    runnerId: 'FOCUS-PROBE',
    allowRealAgents: true,
    label: 'Focus probe (no focus steal on background session create)',
    command: ['pnpm', 'run', 'real-app:focus-probe'],
    // Not part of the serial matrix sweep — only runnable directly (run-soak).
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

// A runner outside the catalog runs outside the matrix, so it keeps whatever
// binaries it needs; the runner says so out loud when it arms nothing.
export function allowRealAgentsForRunner(runnerId, catalog = scenarioCatalog) {
  const entries = typeof runnerId === 'string' && runnerId
    ? catalog.filter((scenario) => scenario.runnerId === runnerId)
    : [];
  if (entries.length === 0) {
    return true;
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
