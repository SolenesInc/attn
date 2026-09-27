import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentWorkspace, daemonSeed, daemonSession } from './test/daemonFixtures';

const SEED = daemonSeed('s-7k3f9m', {
  title: 'Make seed IDs navigable',
  body: 'Recognize **valid** ids and [preview](https://example.test) them.',
  tender_member: 'trellis',
});

async function openTerminalShowing(output: string) {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    workspaces: [agentWorkspace('s1')],
    initialState: { seeds: [SEED] },
    output: { s1: output },
  });
  await act(() => vi.advanceTimersToNextFrame());
  return view;
}

function seedMarks() {
  return [...document.querySelectorAll<HTMLElement>('[data-terminal-seed-id]')].map((mark) => [
    mark.dataset.terminalSeedId,
    mark.style.left,
    mark.style.top,
    mark.style.width,
  ]);
}

describe('App terminal seed ids', () => {
  it('marks the seed ids the garden knows as whole words, on every row a wrapped one spans', async () => {
    await openTerminalShowing(`known s-7k3f9m; unknown s-2m8q4v; xs-7k3f9m s-7k3f9mx\r\n${' '.repeat(76)}s-7k3f9m`);

    expect(seedMarks()).toEqual([
      ['s-7k3f9m', '48px', '0px', '64px'],
      ['s-7k3f9m', '608px', '21px', '32px'],
      ['s-7k3f9m', '0px', '42px', '32px'],
    ]);
  });

  it('previews a marked seed on hover and opens it for the session', async () => {
    const { daemon } = await openTerminalShowing('known s-7k3f9m');

    fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 10 });
    await act(() => vi.advanceTimersByTimeAsync(200));
    const preview = screen.getByRole('dialog');

    expect(within(preview).getByRole('heading')).toHaveTextContent('Make seed IDs navigable');
    expect(preview).toHaveTextContent('Recognize valid ids and preview them.');
    expect(preview).toHaveTextContent('Trellis');

    fireEvent.click(within(preview).getByRole('button', { name: 'Open as tile' }));
    await daemon.idle();

    expect(daemon.sentOf('open_seed')).toEqual([expect.objectContaining({ seed_id: 's-7k3f9m', session_id: 's1' })]);
  });
});
