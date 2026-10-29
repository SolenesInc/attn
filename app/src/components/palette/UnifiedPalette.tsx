import { sessionDisplayName } from '../../utils/sessionDisplayName';
import { useEffect, useMemo, useState, type KeyboardEvent } from 'react';
import FocusTrap from '../AppFocusTrap';
import type { CommandUsage, Desktop } from '../../types/generated';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { isChord, matchesShortcut, type ShortcutId } from '../../shortcuts/registry';
import { resolveBinding } from '../../shortcuts/resolver';
import { SNOOZE_CHOICES, snoozeInstant, type SnoozeChoice } from '../../utils/snoozeDurations';
import { slotShortcut } from '../../utils/desktops';
import { queueActions, type QueueActions } from '../../utils/queueBands';
import { KeyCombos } from '../Keycap';
import { Palette } from './Palette';
import {
  agentPaletteRows,
  isSelectableRow,
  selectableCount,
  type AgentPaletteInput,
  type AgentPaletteRow,
  type PaletteSession,
} from './agentPaletteRows';
import { filterCommands, type PaletteCommand } from './paletteCommands';
import { AgentRowView } from './AgentRows';
import { COMMAND_PREFIX, type PaletteState } from './paletteState';
import './UnifiedPalette.css';

type Item<S extends PaletteSession> =
  | { mode: 'agents'; row: AgentPaletteRow<S> }
  | { mode: 'commands'; command: PaletteCommand }
  | { mode: 'snooze'; choice: SnoozeChoice };

interface UnifiedPaletteProps<S extends PaletteSession> {
  state: PaletteState;
  onStateChange: (state: PaletteState) => void;
  onClose: () => void;
  agents: AgentPaletteInput<S>;
  queueMode: boolean;
  crewInQueue: boolean;
  desktops: readonly Desktop[];
  commands: readonly PaletteCommand[];
  commandUsage?: readonly CommandUsage[];
  commandsLoading?: boolean;
  onCommandPick?: (commandId: string) => void;
  onOpenAgent: (session: S) => void;
  onWakeMember: (member: string) => void;
  onOpenTile: (desktopId: string, tileId: string) => void;
  onSettle: (session: S) => void;
  onPriority?: (session: S) => void;
  onSnooze: (session: S, until: Date) => void;
}

function singleComboBinding(id: ShortcutId) {
  const binding = resolveBinding(id);
  return binding && !isChord(binding) ? binding : null;
}

function pressed(event: KeyboardEvent, id: ShortcutId): boolean {
  const binding = singleComboBinding(id);
  return binding !== null && matchesShortcut(event.nativeEvent, binding);
}

function itemKey<S extends PaletteSession>(item: Item<S>): string {
  if (item.mode === 'agents') return item.row.key;
  if (item.mode === 'commands') return `command:${item.command.id}`;
  return `snooze:${item.choice.id}`;
}

function isSelectable<S extends PaletteSession>(item: Item<S>): boolean {
  return item.mode !== 'agents' || isSelectableRow(item.row);
}

function usePaletteItems<S extends PaletteSession>(
  state: PaletteState,
  agents: AgentPaletteInput<S>,
  commands: readonly PaletteCommand[],
  usage: readonly CommandUsage[],
  loading: boolean,
  actions: (session: S) => QueueActions,
) {
  const { query } = state;
  const commandMode = query.startsWith(COMMAND_PREFIX);
  const allAgentRows = useMemo(() => agentPaletteRows(agents, ''), [agents]);
  const agentRows = useMemo(
    () => (query && !commandMode ? agentPaletteRows(agents, query) : allAgentRows),
    [agents, allAgentRows, commandMode, query],
  );
  const matchingCommands = useMemo(
    () => (commandMode && !loading ? filterCommands(commands, query.slice(COMMAND_PREFIX.length), usage) : []),
    [commandMode, commands, query, usage, loading],
  );
  const snoozedRow = state.mode === 'snooze'
    ? allAgentRows.find((row) => row.kind === 'agent' && row.session.id === state.sessionId)
    : undefined;

  if (state.mode === 'snooze' && snoozedRow?.kind === 'agent' && actions(snoozedRow.session).snooze) {
    return {
      mode: 'snooze' as const,
      snoozing: { session: snoozedRow.session, openedAt: state.openedAt },
      items: SNOOZE_CHOICES.map((choice): Item<S> => ({ mode: 'snooze', choice })),
      count: null,
    };
  }
  if (commandMode) {
    return {
      mode: 'commands' as const,
      snoozing: null,
      items: matchingCommands.map((command): Item<S> => ({ mode: 'commands', command })),
      count: loading ? null : `${matchingCommands.length} of ${commands.length}`,
    };
  }
  return {
    mode: 'agents' as const,
    snoozing: null,
    items: agentRows.map((row): Item<S> => ({ mode: 'agents', row })),
    count: `${selectableCount(agentRows)} of ${selectableCount(allAgentRows)}`,
  };
}

function CommandRowView({ command }: { command: PaletteCommand }) {
  const hasShortcut = command.shortcut?.some((combo) => combo.length > 0) ?? false;
  return (
    <div className="unified-palette-row is-command">
      <span className="unified-palette-icon">{command.icon}</span>
      <span className="unified-palette-name">
        {command.title}
        {command.description && <span className="unified-palette-description">{command.description}</span>}
      </span>
      {command.detail && <span className="unified-palette-age">{command.detail}</span>}
      {hasShortcut && command.shortcut && <KeyCombos combos={command.shortcut} />}
    </div>
  );
}

function InPaletteAction({ id, label }: { id: ShortcutId; label: string }) {
  if (singleComboBinding(id)) return <><b>{formatShortcut(id)}</b> {label}</>;
  return <>{label} needs a single-key shortcut (chords don&apos;t work here)</>;
}

function PaletteFooter({ mode, actions }: { mode: 'agents' | 'commands' | 'snooze'; actions: QueueActions }) {
  if (mode === 'snooze') {
    return (
      <>
        <span><b>↑↓</b> move</span>
        <span><b>↵</b> snooze</span>
        <span><b>esc</b> back to agents</span>
      </>
    );
  }
  if (mode === 'commands') {
    return (
      <>
        <span><b>↑↓</b> move</span>
        <span><b>↵</b> run</span>
        <span>delete <b>{COMMAND_PREFIX}</b> to search agents</span>
        <span><b>esc</b> closes</span>
      </>
    );
  }
  return (
    <>
      <span><b>↑↓</b> move</span>
      <span><b>↵</b> jump</span>
      <span>type <b>{COMMAND_PREFIX}</b> for commands</span>
      {actions.settle && <span data-testid="palette-settle-hint"><InPaletteAction id="session.settle" label="settle" /></span>}
      {actions.snooze && <span data-testid="palette-snooze-hint"><InPaletteAction id="session.snooze" label="snooze" /></span>}
      <span><b>esc</b> closes</span>
    </>
  );
}

function focusedElement(): HTMLElement | null {
  const active = document.activeElement;
  return active instanceof HTMLElement && active !== document.body ? active : null;
}

export function UnifiedPalette<S extends PaletteSession>({
  state,
  onStateChange,
  onClose,
  agents,
  queueMode,
  crewInQueue,
  desktops,
  commands,
  commandUsage = [],
  commandsLoading = false,
  onCommandPick,
  onOpenAgent,
  onWakeMember,
  onOpenTile,
  onSettle,
  onPriority,
  onSnooze,
}: UnifiedPaletteProps<S>) {
  const [agentKeyAfterSnooze, setAgentKeyAfterSnooze] = useState<string | null>(null);
  const actions = (session: S) => queueActions(session, { queueMode, crewInQueue, now: agents.now });
  const itemActions = (item: Item<S> | undefined) => item?.mode === 'agents' && item.row.kind === 'agent'
    ? actions(item.row.session) : { settle: false, snooze: false };
  const { mode, snoozing, items, count } = usePaletteItems(state, agents, commands, commandUsage, commandsLoading, actions);
  useEffect(() => {
    if (state.mode === 'snooze' && !snoozing) onStateChange({ mode: 'search', query: state.query });
  }, [state, snoozing, onStateChange]);
  const leaveSnooze = (session: S) => {
    setAgentKeyAfterSnooze(`agent:${session.id}`);
    onStateChange({ mode: 'search', query: state.query });
  };

  const desktopOfSession = useMemo(() => {
    const byId = new Map<string, string>();
    for (const desktop of agents.desktops) {
      for (const session of desktop.sessions) byId.set(session.id, desktop.id);
    }
    return byId;
  }, [agents.desktops]);
  const slotOf = (desktopId: string | undefined, sessionId?: string) => {
    const id = sessionId ? desktopOfSession.get(sessionId) : desktopId;
    const desktop = desktops.find((entry) => entry.id === id);
    if (!desktop) return '—';
    return desktop.shortcut_slot ? slotShortcut(desktop.shortcut_slot) : '·';
  };

  const [opener] = useState(focusedElement);

  const pick = (item: Item<S>) => {
    if (item.mode === 'snooze') {
      if (!snoozing || !actions(snoozing.session).snooze) return;
      onSnooze(snoozing.session, snoozeInstant(item.choice.id, snoozing.openedAt));
      leaveSnooze(snoozing.session);
      return;
    }
    if (item.mode === 'commands') onCommandPick?.(item.command.id);
    onClose();
    if (item.mode === 'commands') {
      item.command.run(opener);
      return;
    }
    const { row } = item;
    if (row.kind === 'agent') onOpenAgent(row.session);
    else if (row.kind === 'member') onWakeMember(row.member);
    else if (row.kind === 'tile') onOpenTile(row.desktopId, row.tile.tileId);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>, highlighted: Item<S> | undefined) => {
    if (snoozing) return false;
    const agent = highlighted?.mode === 'agents' && highlighted.row.kind === 'agent' ? highlighted.row.session : null;
    if (pressed(event, 'session.settle')) {
      event.preventDefault();
      if (agent && itemActions(highlighted).settle) onSettle(agent);
      return true;
    }
    if (pressed(event, 'session.priority')) {
      event.preventDefault();
      if (agent) onPriority?.(agent);
      return true;
    }
    if (pressed(event, 'session.snooze')) {
      event.preventDefault();
      if (agent && itemActions(highlighted).snooze) {
        onStateChange({ mode: 'snooze', sessionId: agent.id, openedAt: new Date(), query: state.query });
      }
      return true;
    }
    return false;
  };

  const renderItem = (item: Item<S>) => {
    if (item.mode === 'agents') return <AgentRowView row={item.row} now={agents.now} slotOf={slotOf} />;
    if (item.mode === 'commands') return <CommandRowView command={item.command} />;
    return (
      <div className="unified-palette-row">
        <span className="unified-palette-name">{item.choice.label}</span>
        <span className="unified-palette-age">{snoozing && item.choice.detail(snoozing.openedAt)}</span>
      </div>
    );
  };

  return (
    <FocusTrap focusTrapOptions={{
      allowOutsideClick: true,
      escapeDeactivates: false,
      setReturnFocus: (previous: HTMLElement | SVGElement) => (
        document.activeElement === document.body ? previous : false
      ),
    }}>
      <div className="unified-palette-root">
        <Palette<Item<S>>
          key={snoozing ? 'snooze' : 'list'}
          initialSelectedKey={snoozing ? null : agentKeyAfterSnooze}
          variant="unified-palette"
          ariaLabel={snoozing ? `Snooze ${sessionDisplayName(snoozing.session)}` : mode === 'commands' ? 'Commands' : 'Agents'}
          placeholder={snoozing ? `Snooze ${sessionDisplayName(snoozing.session)}` : 'Jump to an agent or tile · type > for commands'}
          query={snoozing ? '' : state.query}
          onQueryChange={(query) => onStateChange({ mode: 'search', query })}
          items={items}
          itemKey={itemKey}
          renderItem={renderItem}
          isSelectable={isSelectable}
          emptyLabel={mode === 'commands' ? commandsLoading ? 'Loading command history…' : 'No matching commands' : 'No match'}
          onPick={pick}
          onClose={onClose}
          onEscape={snoozing ? () => leaveSnooze(snoozing.session) : onClose}
          onKeyDown={handleKeyDown}
          inputPrefix={
            <kbd className="unified-palette-mode">
              {formatShortcut(mode === 'commands' ? 'ui.commandPalette' : 'ui.actionMenu')}
            </kbd>
          }
          inputSuffix={count !== null && <span className="unified-palette-count">{count}</span>}
          footer={(highlighted) => <PaletteFooter mode={mode} actions={itemActions(highlighted)} />}
        />
      </div>
    </FocusTrap>
  );
}
