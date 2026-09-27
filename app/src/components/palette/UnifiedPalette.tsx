import { useMemo, useState, type KeyboardEvent } from 'react';
import FocusTrap from 'focus-trap-react';
import type { Desktop } from '../../types/generated';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { isChord, matchesShortcut, type ShortcutId } from '../../shortcuts/registry';
import { resolveBinding } from '../../shortcuts/resolver';
import { formatTurnAge } from '../../utils/queueBands';
import { isSnoozed, SNOOZE_CHOICES, snoozeInstant, type SnoozeChoice } from '../../utils/snoozeDurations';
import { slotShortcut } from '../../utils/desktops';
import { crewDisplayName } from '../../utils/crewName';
import { KeyCombos } from '../Keycap';
import { Palette } from './Palette';
import {
  agentPaletteRows,
  agentStatus,
  isSelectableRow,
  selectableCount,
  type AgentPaletteInput,
  type AgentPaletteRow,
  type PaletteSession,
} from './agentPaletteRows';
import { filterCommands, type PaletteCommand } from './paletteCommands';
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
  desktops: readonly Desktop[];
  commands: readonly PaletteCommand[];
  onOpenAgent: (session: S) => void;
  onWakeMember: (member: string) => void;
  onOpenTile: (desktopId: string, tileId: string) => void;
  onSettle: (session: S) => void;
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
) {
  const { query } = state;
  const commandMode = query.startsWith(COMMAND_PREFIX);
  const allAgentRows = useMemo(() => agentPaletteRows(agents, ''), [agents]);
  const agentRows = useMemo(
    () => (query && !commandMode ? agentPaletteRows(agents, query) : allAgentRows),
    [agents, allAgentRows, commandMode, query],
  );
  const matchingCommands = useMemo(
    () => (commandMode ? filterCommands(commands, query.slice(COMMAND_PREFIX.length)) : []),
    [commandMode, commands, query],
  );
  const snoozedRow = state.mode === 'snooze'
    ? allAgentRows.find((row) => row.kind === 'agent' && row.session.id === state.sessionId)
    : undefined;

  if (state.mode === 'snooze' && snoozedRow?.kind === 'agent') {
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
      count: `${matchingCommands.length} of ${commands.length}`,
    };
  }
  return {
    mode: 'agents' as const,
    snoozing: null,
    items: agentRows.map((row): Item<S> => ({ mode: 'agents', row })),
    count: `${selectableCount(agentRows)} of ${selectableCount(allAgentRows)}`,
  };
}

function AgentRowView<S extends PaletteSession>({
  row,
  now,
  slotOf,
}: {
  row: AgentPaletteRow<S>;
  now: number;
  slotOf: (desktopId: string | undefined, sessionId?: string) => string;
}) {
  switch (row.kind) {
    case 'divider':
      return <hr className="unified-palette-divider" />;
    case 'runs':
      return (
        <div className="unified-palette-runs" data-testid={`palette-runs-${row.key}`}>
          <span className="unified-palette-name">{row.name}</span>
          <span className="unified-palette-runs-count">
            {row.needYou > 0 ? `${row.needYou} need you · ` : ''}
            {row.runs} run{row.runs === 1 ? '' : 's'} · never in the queue
          </span>
        </div>
      );
    case 'member':
      return (
        <div className="unified-palette-row">
          <span className="unified-palette-dot is-asleep" />
          <span className="unified-palette-name">
            {crewDisplayName(row.member)} <span className="unified-palette-muted">· crew</span>
          </span>
          <kbd className="unified-palette-slot is-unplaced">—</kbd>
          <span className="unified-palette-pill">asleep</span>
          <span className="unified-palette-age">wake</span>
          <span className="unified-palette-tag" />
        </div>
      );
    case 'tile':
      return (
        <div className="unified-palette-row">
          <span className="unified-palette-dot is-tile" />
          <span className="unified-palette-name">{row.title}</span>
          <kbd className="unified-palette-slot">{slotOf(row.desktopId)}</kbd>
          <span className="unified-palette-pill is-tile">{row.tile.tileKind === 'markdown' ? 'doc' : 'tile'}</span>
          <span className="unified-palette-age" />
          <span className="unified-palette-tag" />
        </div>
      );
    case 'agent':
      return <AgentSessionRow session={row.session} queueHead={row.queueHead} now={now} slot={slotOf(undefined, row.session.id)} />;
  }
}

function AgentSessionRow({ session, queueHead, now, slot }: { session: PaletteSession; queueHead: boolean; now: number; slot: string }) {
  const status = agentStatus(session, now);
  const owedAge = session.turnOwed && !isSnoozed(session.turnSnoozedUntil, now);
  return (
    <div className="unified-palette-row" data-testid={`palette-agent-${session.id}`}>
      <span className={`unified-palette-dot is-${status}`} />
      <span className="unified-palette-name">
        {session.label}
        {session.crewMember && !session.chiefOfStaff && <span className="unified-palette-muted"> · crew</span>}
      </span>
      <kbd className="unified-palette-slot">{slot}</kbd>
      <span className={`unified-palette-pill is-${status}`}>{status}</span>
      <span className="unified-palette-age">{owedAge ? formatTurnAge(session.turnOpenedAt, now) : ''}</span>
      <span className="unified-palette-tag">{queueHead && <kbd>{formatShortcut('session.jumpToWaiting')}</kbd>}</span>
    </div>
  );
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

function PaletteFooter({ mode }: { mode: 'agents' | 'commands' | 'snooze' }) {
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
      <span data-testid="palette-settle-hint"><InPaletteAction id="session.settle" label="settle" /></span>
      <span data-testid="palette-snooze-hint"><InPaletteAction id="session.snooze" label="snooze" /></span>
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
  desktops,
  commands,
  onOpenAgent,
  onWakeMember,
  onOpenTile,
  onSettle,
  onSnooze,
}: UnifiedPaletteProps<S>) {
  const [agentKeyAfterSnooze, setAgentKeyAfterSnooze] = useState<string | null>(null);
  const { mode, snoozing, items, count } = usePaletteItems(state, agents, commands);
  const leaveSnooze = (session: S) => {
    setAgentKeyAfterSnooze(`agent:${session.id}`);
    onStateChange({ mode: 'search', query: state.query });
  };

  const desktopOfSession = useMemo(() => {
    const byId = new Map<string, string>();
    for (const workspace of agents.workspaces) {
      for (const session of workspace.sessions) byId.set(session.id, workspace.id);
    }
    return byId;
  }, [agents.workspaces]);
  const slotOf = (desktopId: string | undefined, sessionId?: string) => {
    const id = sessionId ? desktopOfSession.get(sessionId) : desktopId;
    const desktop = desktops.find((entry) => entry.id === id);
    if (!desktop) return '—';
    return desktop.shortcut_slot ? slotShortcut(desktop.shortcut_slot) : '·';
  };

  const [opener] = useState(focusedElement);

  const pick = (item: Item<S>) => {
    if (item.mode === 'snooze') {
      if (!snoozing) return;
      onSnooze(snoozing.session, snoozeInstant(item.choice.id, snoozing.openedAt));
      leaveSnooze(snoozing.session);
      return;
    }
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
      if (agent?.turnOwed) onSettle(agent);
      return true;
    }
    if (pressed(event, 'session.snooze')) {
      event.preventDefault();
      if (agent && !agent.chiefOfStaff) {
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
          ariaLabel={snoozing ? `Snooze ${snoozing.session.label}` : mode === 'commands' ? 'Commands' : 'Agents'}
          placeholder={snoozing ? `Snooze ${snoozing.session.label}` : 'Jump to an agent or tile · type > for commands'}
          query={snoozing ? '' : state.query}
          onQueryChange={(query) => onStateChange({ mode: 'search', query })}
          items={items}
          itemKey={itemKey}
          renderItem={renderItem}
          isSelectable={isSelectable}
          emptyLabel={mode === 'commands' ? 'No matching commands' : 'No match'}
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
          footer={<PaletteFooter mode={mode} />}
        />
      </div>
    </FocusTrap>
  );
}
