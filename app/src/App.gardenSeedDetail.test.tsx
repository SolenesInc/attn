import { fireEvent, screen, within } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { describe, expect, it } from 'vitest';
import { daemonSession } from './test/daemonFixtures';
import { gardenRegion, openGarden, openRow, partOf, planted, plot, row, seedHeading } from './test/garden';
import { gesture } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const PR_42 = {
  pull_request: 'github.com:victorarias/attn#42',
  url: 'https://github.com/victorarias/attn/pull/42',
  set_at: '2026-09-02T10:00:00Z',
};

const related = () => within(screen.getByRole('heading', { name: 'Related' }).closest('section')!);
const log = () => within(screen.getByRole('heading', { name: 'Log' }).closest('section')!);

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(within(gardenRegion()).getByRole('button', { name })));
}

function note(id: string, body: string) {
  return { id, seed_id: 's-plan11', kind: 'note', body, author_session: 'sess-a', author_member: 'trellis', created_at: '2026-08-15T09:00:00Z' };
}

describe('App garden seed detail', () => {
  describe('blockers and relations', () => {
    const crown = plot('s-ccc111', 'the crown', '', { total: 1, ready: 0, blocked: 1 });
    const blocked = partOf('s-ccc111', 's-bbb111', 'the blocked one');
    const blocker = (status: string) => planted('s-aaa111', 'the blocker', { status, ready: status === 'planted', edges: [{ kind: 'blocks', to: 's-bbb111' }] });

    it('says how many open seeds block a seed, and nothing about one that is free', async () => {
      const garden = await openGarden([blocker('planted'), blocked, crown]);
      expect(row('the blocker')).not.toHaveTextContent(/ready|blocked/);

      await openRow(garden.daemon, 'the crown');
      expect(row('the blocked one')).toHaveTextContent('blocked by 1');

      garden.push([blocker('harvested'), blocked, crown]);
      expect(row('the blocked one')).not.toHaveTextContent('blocked by');
    });

    it('counts a blocker from outside the plot it is standing in', async () => {
      const away = planted('s-ddd111', 'blocker elsewhere', { edges: [{ kind: 'blocks', to: 's-bbb111' }] });
      const { daemon } = await openGarden([blocked, away, crown]);

      await openRow(daemon, 'the crown');

      expect(row('blocker elsewhere')).toBeNull();
      expect(row('the blocked one')).toHaveTextContent('blocked by 1');
    });

    it('lists an opened seed’s edges in both directions', async () => {
      const { daemon } = await openGarden([blocker('planted'), blocked, crown]);

      await openRow(daemon, 'the crown');
      await openRow(daemon, 'the blocked one');

      expect(related().getByText('part of').closest('li')).toHaveTextContent('the crown');
      expect(related().getByText('blocked by').closest('li')).toHaveTextContent('the blocker');
    });

    it('shows a discovered-from edge from the work and from its origin', async () => {
      const origin = planted('s-origin1', 'the origin');
      const found = planted('s-found11', 'the discovered work', { edges: [{ kind: 'discovered-from', to: 's-origin1' }] });
      const { daemon } = await openGarden([found, origin]);

      await openRow(daemon, 'the discovered work');
      expect(related().getByText('discovered from').closest('li')).toHaveTextContent('the origin');

      await click(daemon, 'the origin');
      expect(seedHeading()).toBe('the origin');
      expect(related().getByText('discovered').closest('li')).toHaveTextContent('the discovered work');
    });
  });

  describe('harvest condition', () => {
    it('says what an armed row waits on where a parked row says parked', async () => {
      await openGarden([
        planted('s-armed1', 'waiting on the merge', { status: 'dormant', harvest_when: PR_42 }),
        planted('s-park11', 'put down for now', { status: 'dormant' }),
        planted('s-blank1', 'armed with nothing', { harvest_when: { ...PR_42, pull_request: '  ' } }),
      ]);

      expect(row('waiting on the merge')).toHaveTextContent('harvests on #42');
      expect(row('waiting on the merge')).not.toHaveTextContent('parked');
      expect(within(row('waiting on the merge')!).getByText('harvests on #42'))
        .toHaveAttribute('title', 'harvests when victorarias/attn#42 merges');
      expect(row('put down for now')).toHaveTextContent('parked');
      expect(row('put down for now')).not.toHaveTextContent('harvests on');
      expect(row('armed with nothing')).not.toHaveTextContent('harvests on');
    });

    it('says the whole condition on the seed it opens, and opens the pull request', async () => {
      const { daemon } = await openGarden([planted('s-armed1', 'waiting on the merge', { status: 'dormant', harvest_when: PR_42 })]);
      await openRow(daemon, 'waiting on the merge');

      const link = within(gardenRegion()).getByRole('link', { name: /harvests when victorarias\/attn#42 merges/ });
      expect(link).toHaveAttribute('href', PR_42.url);
      fireEvent.click(link);

      expect(openUrl).toHaveBeenCalledWith(PR_42.url);
    });

    it('keeps a pull request it cannot take apart rather than showing nothing', async () => {
      const { daemon } = await openGarden([planted('s-armed2', 'waiting elsewhere', { harvest_when: { pull_request: 'somewhere-else', url: '', set_at: '' } })]);
      expect(row('waiting elsewhere')).toHaveTextContent('harvests on somewhere-else');

      await openRow(daemon, 'waiting elsewhere');

      expect(within(gardenRegion()).getByText('harvests when somewhere-else merges')).toBeInTheDocument();
      expect(within(gardenRegion()).queryByRole('link', { name: /harvests when/ })).toBeNull();
    });
  });

  describe('lifecycle', () => {
    it('shows a seed’s state and who tends it in the row', async () => {
      await openGarden(
        [
          planted('s-grow11', 'tended by crew', { status: 'growing', tender_member: 'trellis', tender_session: 'sess-a' }),
          planted('s-grow22', 'claimed by a session', { status: 'growing', tender_session: 'sess-b' }),
          planted('s-grow33', 'claimed by an unknown session', { status: 'growing', tender_session: '4915e44d-fadd-4dc8-82cf-671cbbf872c0' }),
          planted('s-idle11', 'unclaimed'),
        ],
        { sessions: [daemonSession('s1'), daemonSession('sess-b', { label: 'Garden polish' })] },
      );

      expect(row('tended by crew')).toHaveTextContent('growing');
      expect(row('tended by crew')).toHaveTextContent('tended by Trellis');
      expect(row('claimed by a session')).toHaveTextContent('tended by Garden polish');
      expect(row('claimed by an unknown session')).toHaveTextContent('tended by session');
      expect(row('claimed by an unknown session')).not.toHaveTextContent('4915e44d');
      expect(row('unclaimed')).not.toHaveTextContent('tended by');
    });

    it('follows a seed through its life as the pushes arrive, and says why it closed once opened', async () => {
      const life = planted('s-life11', 'a whole life');
      const garden = await openGarden([life]);
      expect(row('a whole life')).not.toHaveTextContent('planted');

      garden.push([{ ...life, status: 'growing', tender_member: 'trellis', rev: 2 }]);
      expect(row('a whole life')).toHaveTextContent('growing');
      expect(row('a whole life')).toHaveTextContent('tended by Trellis');

      garden.push([{ ...life, status: 'harvested', reason: 'shipped it', rev: 3 }]);
      expect(row('a whole life')).toBeNull();
      await click(garden.daemon, '1 closed');
      expect(row('a whole life')).toHaveTextContent('done');
      expect(row('a whole life')).not.toHaveTextContent('tended by');
      expect(screen.queryByText('shipped it')).toBeNull();

      await openRow(garden.daemon, 'a whole life');
      expect(screen.getByText('shipped it')).toBeInTheDocument();
    });

    it('says what is hidden when everything in view is closed', async () => {
      const { daemon } = await openGarden([
        planted('s-done22', 'all wrapped', { status: 'harvested' }),
        planted('s-dead11', 'went nowhere', { status: 'withered' }),
      ]);
      expect(screen.getByText(/Nothing open here\. 2 closed seeds are/)).toBeInTheDocument();

      await click(daemon, '2 closed');

      expect(row('all wrapped')).not.toBeNull();
      expect(row('went nowhere')).not.toBeNull();
      expect(screen.queryByText(/Nothing open here/)).toBeNull();
    });
  });

  describe('the opened seed', () => {
    const plan = planted('s-plan11', 'Open this plan', { body: '# First body', status: 'growing', tender_member: 'trellis' });

    it('reads the seed’s document and opens a linked markdown file from it', async () => {
      const garden = await openGarden([plan]);
      garden.documents[plan.id] = {
        notes: [note('n-live', 'The live log entry')],
        notes_total: 1,
        references: [{ kind: 'markdown_file', path: '/repo/evidence.md' }],
      };
      garden.daemon.on('fs_exists', ({ path, root = '' }) => ({ event: 'fs_exists_result', success: true, result: { path: `${root}/${path}`, exists: true } }));

      await openRow(garden.daemon, 'Open this plan');

      expect(garden.daemon.sentOf('seed_document_get')).toEqual([expect.objectContaining({ seed_id: plan.id })]);
      expect(within(gardenRegion()).getByRole('heading', { name: 'First body' })).toBeInTheDocument();
      expect(log().getByText('The live log entry')).toBeInTheDocument();
      const artifact = within(gardenRegion()).getByRole('button', { name: /evidence\.md/ });
      expect(artifact.closest('li')).toHaveTextContent('linked file');
      await gesture(garden.daemon, () => fireEvent.click(artifact));
      expect(garden.daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ path: '/repo/evidence.md' })]);
    });

    it('reads the open seed again on a garden push even when its revision is unchanged', async () => {
      const garden = await openGarden([plan]);
      garden.documents[plan.id] = { notes: [note('n-before', 'Before the push')], notes_total: 1 };
      await openRow(garden.daemon, 'Open this plan');
      expect(log().getByText('Before the push')).toBeInTheDocument();

      garden.documents[plan.id] = { notes: [note('n-after', 'After the push')], notes_total: 1 };
      await gesture(garden.daemon, () => garden.push([{ ...plan }]));

      expect(log().getByText('After the push')).toBeInTheDocument();
      expect(garden.daemon.sentOf('seed_document_get')).toHaveLength(2);
    });

    it('shows the daemon’s error when the seed cannot be read', async () => {
      const garden = await openGarden([plan]);
      garden.documents[plan.id] = new Error('no seed s-plan11 is planted here');

      await openRow(garden.daemon, 'Open this plan');

      expect(log().getByText('no seed s-plan11 is planted here')).toBeInTheDocument();
    });
  });
});
