import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { buildQueueBands } from '../../utils/queueBands';
import type { WorkspaceWithSessions } from '../../utils/workspaceViewModels';
import type { PaletteSession } from './agentPaletteRows';
import type { PaletteCommand } from './paletteCommands';
import { UnifiedPalette } from './UnifiedPalette';

const NOW = Date.parse('2026-09-26T12:00:00Z');

function session(id: string, fields: Partial<PaletteSession> = {}): PaletteSession {
  return { id, label: id, state: 'idle', ...fields };
}

function workspaces(sessions: PaletteSession[]): WorkspaceWithSessions<PaletteSession>[] {
  return [{
    id: 'd1',
    title: 'Desktop 1',
    directory: '/tmp',
    sessions,
    children: sessions.map((entry) => ({ kind: 'session' as const, id: entry.id, session: entry })),
    firstSessionId: sessions[0]?.id ?? null,
    focusedSessionId: null,
    hasUnresolvedAgentPanes: false,
  }];
}

const FIXTURE = [
  session('chief', { chiefOfStaff: true }),
  session('owed', { turnOwed: true, turnOpenedAt: new Date(NOW - 120_000).toISOString() }),
  session('quiet'),
  session('nightly-run', { automation: { definition_id: 'nightly', definition_name: 'Nightly' }, turnOwed: true }),
];

function renderPalette({
  sessions = FIXTURE,
  initialQuery = '',
  commands = [] as PaletteCommand[],
} = {}) {
  const handlers = {
    onClose: vi.fn(),
    onOpenAgent: vi.fn(),
    onWakeMember: vi.fn(),
    onOpenTile: vi.fn(),
    onSettle: vi.fn(),
    onSnooze: vi.fn(),
  };
  function Harness({ current }: { current: PaletteSession[] }) {
    const [query, setQuery] = useState(initialQuery);
    const views = workspaces(current);
    return (
      <UnifiedPalette
        query={query}
        onQueryChange={setQuery}
        agents={{
          bands: buildQueueBands(views, { now: NOW }),
          crewRoster: [],
          workspaces: views,
          tileTitle: () => '',
          now: NOW,
        }}
        desktops={[]}
        commands={commands}
        {...handlers}
      />
    );
  }
  const view = render(<Harness current={sessions} />);
  const input = () => screen.getByRole('combobox');
  const highlighted = () => screen.getByRole('option', { selected: true }).textContent;
  return {
    ...handlers,
    input,
    highlighted,
    rerender: (next: PaletteSession[]) => view.rerender(<Harness current={next} />),
  };
}

describe('UnifiedPalette keyboard flow', () => {
  it('walks agents and runs while stepping over the divider and definition headers', () => {
    const palette = renderPalette();
    expect(palette.highlighted()).toContain('chief');
    expect(screen.getByText('4 of 4')).toBeInTheDocument();
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    expect(palette.highlighted()).toContain('owed');
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    expect(palette.highlighted()).toContain('nightly-run');
    expect(screen.getByText(/1 need you · 1 run · never in the queue/)).toBeInTheDocument();
    fireEvent.keyDown(palette.input(), { key: 'Enter' });
    expect(palette.onOpenAgent).toHaveBeenCalledWith(expect.objectContaining({ id: 'nightly-run' }));
    expect(palette.onClose).toHaveBeenCalled();
  });

  it('settles and snoozes the highlighted agent without closing', () => {
    const palette = renderPalette();
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    fireEvent.keyDown(palette.input(), { key: 'e', metaKey: true, shiftKey: true });
    expect(palette.onSettle).toHaveBeenCalledWith(expect.objectContaining({ id: 'owed' }));

    fireEvent.keyDown(palette.input(), { key: 's', metaKey: true, shiftKey: true });
    expect(screen.getByRole('dialog', { name: 'Snooze owed' })).toBeInTheDocument();
    fireEvent.keyDown(palette.input(), { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Agents' })).toBeInTheDocument();
    expect(palette.highlighted()).toContain('owed');
    expect(palette.onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(palette.input(), { key: 's', metaKey: true, shiftKey: true });
    fireEvent.keyDown(palette.input(), { key: 'Enter' });
    expect(palette.onSnooze).toHaveBeenCalledWith(expect.objectContaining({ id: 'owed' }), expect.any(Date));
    expect(screen.getByRole('dialog', { name: 'Agents' })).toBeInTheDocument();
    expect(palette.onClose).not.toHaveBeenCalled();
  });

  it('does not settle an agent that owes no turn', () => {
    const palette = renderPalette();
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    expect(palette.highlighted()).toContain('quiet');
    fireEvent.keyDown(palette.input(), { key: 'e', metaKey: true, shiftKey: true });
    expect(palette.onSettle).not.toHaveBeenCalled();
  });

  it('keeps the highlight on the same agent when live updates reorder the rows', () => {
    const palette = renderPalette();
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    fireEvent.keyDown(palette.input(), { key: 'ArrowDown' });
    expect(palette.highlighted()).toContain('quiet');
    palette.rerender([
      session('chief', { chiefOfStaff: true }),
      session('new-turn', { turnOwed: true, turnOpenedAt: new Date(NOW - 600_000).toISOString() }),
      ...FIXTURE.slice(1),
    ]);
    expect(palette.highlighted()).toContain('quiet');
  });

  it('switches to commands on > and back when it is deleted', () => {
    const run = vi.fn();
    const palette = renderPalette({ commands: [{ id: 'home', title: 'Home', run }] });
    fireEvent.change(palette.input(), { target: { value: '>' } });
    expect(screen.getByRole('dialog', { name: 'Commands' })).toBeInTheDocument();
    expect(screen.getByText('1 of 1')).toBeInTheDocument();
    fireEvent.change(palette.input(), { target: { value: '' } });
    expect(screen.getByRole('dialog', { name: 'Agents' })).toBeInTheDocument();
    fireEvent.change(palette.input(), { target: { value: '>ho' } });
    fireEvent.keyDown(palette.input(), { key: 'Enter' });
    expect(palette.onClose).toHaveBeenCalled();
    expect(run).toHaveBeenCalled();
  });
});
