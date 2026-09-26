import { fireEvent, screen, within } from '@testing-library/react';
import {
  agentWorkspace,
  daemonSeed,
  daemonSession,
  seedDocument,
  type DaemonSeed,
  type DaemonSeedDocument,
  type DaemonSession,
} from './daemonFixtures';
import type { CommandMessage, EventMessage } from './protocol';
import { gesture, pressShortcut, renderApp } from './renderApp';
import type { Reply, ScriptedDaemon } from './scriptedDaemon';

type InitialState = EventMessage<'initial_state'>;
type PlotProgress = NonNullable<DaemonSeed['plot_progress']>;

export function plot(id: string, title: string, parent = '', progress: Partial<PlotProgress> = {}, overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return daemonSeed(id, {
    title,
    status: 'planted',
    edges: parent ? [{ kind: 'part-of', to: parent }] : [],
    plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0, ...progress },
    ...overrides,
  });
}

export function planted(id: string, title: string, overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return daemonSeed(id, { title, status: 'planted', ...overrides });
}

export function partOf(parent: string, id: string, title: string, overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return planted(id, title, { ...overrides, edges: [{ kind: 'part-of', to: parent }, ...(overrides.edges ?? [])] });
}

export interface Garden {
  daemon: ScriptedDaemon;
  unmount(): void;
  seeds: DaemonSeed[];
  documents: Record<string, Partial<DaemonSeedDocument> | Error>;
  push(seeds: DaemonSeed[], total?: number): void;
  answer(read: CommandMessage<'seed_document_get'>): Reply;
}

export async function renderGarden(
  seeds: DaemonSeed[],
  { sessions = [daemonSession('s1')], total, initial = {} }: { sessions?: DaemonSession[]; total?: number; initial?: Partial<InitialState> } = {},
): Promise<Garden> {
  const { daemon, unmount } = await renderApp({
    initialState: { sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)), seeds, seeds_total: total, ...initial },
  });
  const garden: Garden = {
    daemon,
    unmount,
    seeds,
    documents: {},
    push(next, pushedTotal = next.length) {
      garden.seeds = next;
      daemon.emit({ event: 'garden_seeds_updated', seeds: next, total: pushedTotal });
    },
    answer({ seed_id, request_id }) {
      const seed = garden.seeds.find((candidate) => candidate.id === seed_id);
      const document = garden.documents[seed_id] ?? {};
      if (!seed) return { event: 'seed_document_get_result', request_id, success: false, error: `seed ${seed_id} not found` };
      if (document instanceof Error) return { event: 'seed_document_get_result', request_id, success: false, error: document.message };
      return { event: 'seed_document_get_result', request_id, success: true, document: seedDocument(seed, document) };
    },
  };
  daemon.on('seed_document_get', (read) => garden.answer(read));
  return garden;
}

export async function openGarden(seeds: DaemonSeed[], options: Parameters<typeof renderGarden>[1] = {}): Promise<Garden> {
  const garden = await renderGarden(seeds, options);
  await gesture(garden.daemon, () => pressShortcut('board.open'));
  return garden;
}

export function gardenRegion(): HTMLElement {
  return screen.getByRole('region', { name: 'The garden' });
}

export function trail(): string[] {
  const nav = screen.getByRole('navigation', { name: /The way back|Standing here/ });
  return within(nav).queryAllByRole('button').map((step) => step.textContent?.trim() ?? '');
}

const rowName = (title: string) => new RegExp(`^${title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}.`);

export function row(title: string): HTMLElement | null {
  return within(gardenRegion()).queryByRole('button', { name: rowName(title) })
    ?? within(gardenRegion()).queryByRole('option', { name: rowName(title) });
}

export async function openRow(daemon: ScriptedDaemon, title: string) {
  const target = row(title);
  if (!target) throw new Error(`no Garden row for ${title}`);
  await gesture(daemon, () => fireEvent.click(target));
}

export function seedHeading(): string | null {
  return within(gardenRegion()).queryByRole('heading', { level: 2 })?.textContent ?? null;
}
