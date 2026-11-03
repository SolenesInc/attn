import { act } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonDesktop, daemonSession, defaultProfile } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

const SOURCE = 'desktop-source';
const TARGET = 'desktop-target';

function agentDesktop(id: string, slot: number, sessionIds: string[], revision = 1) {
  const panes = sessionIds.map((sessionId) => agentPane(sessionId, id));
  const leaves = panes.map((pane) => ({ type: 'pane', pane_id: pane.pane_id }));
  const root = leaves.length === 1
    ? leaves[0]
    : { type: 'split', split_id: `${id}-split`, direction: 'horizontal', ratio: 0.5, children: leaves };
  return daemonDesktop(id, { root, panes }, { shortcut_slot: slot, revision });
}

function renderedPanes(desktopId: string): string[] {
  const surface = document.querySelector(`.session-terminal-desktop[data-desktop-id="${desktopId}"]`);
  return Array.from(surface?.querySelectorAll('[data-pane-kind="agent"]') ?? [])
    .map((node) => node.getAttribute('data-pane-session-id') || '')
    .sort();
}

describe('a pane moved to another desktop', () => {
  it('keeps a moved agent on its new desktop when a stale session broadcast arrives', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: ['s-stay', 's-moved', 's-target'].map((id) => daemonSession(id)),
        profiles: [defaultProfile(SOURCE)],
        desktops: [agentDesktop(SOURCE, 1, ['s-stay', 's-moved']), agentDesktop(TARGET, 2, ['s-target'])],
      },
    });
    expect(renderedPanes(SOURCE)).toEqual(['s-moved', 's-stay']);

    act(() => {
      daemon.emit({
        event: 'profile_arrangement_changed',
        profile: defaultProfile(TARGET, { revision: 2 }),
        desktops: [agentDesktop(SOURCE, 1, ['s-stay'], 2), agentDesktop(TARGET, 2, ['s-target', 's-moved'], 2)],
      });
      daemon.emit({ event: 'session_state_changed', session: daemonSession('s-moved') });
    });
    await daemon.idle();

    expect(renderedPanes(TARGET)).toEqual(['s-moved', 's-target']);
    expect(renderedPanes(SOURCE)).toEqual(['s-stay']);
  });
});
