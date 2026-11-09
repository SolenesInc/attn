import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { memberClaim, USER_ACTOR, type DaemonSeed } from './test/daemonFixtures';
import { gardenRegion, openGarden, planted, type Garden } from './test/garden';
import { gesture, pressShortcut } from './test/renderApp';

type Standby = NonNullable<DaemonSeed['standby']>;

const SET_AT = '2026-11-05T09:00:00Z';

function watching(readiness: string, state = 'open'): Standby['pull_requests'][number] {
  return {
    pr_id: 'github.com:victorarias/attn#123',
    url: 'https://github.com/victorarias/attn/pull/123',
    state,
    readiness_state: readiness,
  };
}

function remindAtTonight() {
  const at = new Date();
  at.setHours(23, 30, 0, 0);
  return { at: at.toISOString(), message: 'check the rollout', set_at: SET_AT, set_by: USER_ACTOR };
}

function onStandby(overrides: Partial<Standby> = {}, seed: Partial<DaemonSeed> = {}): DaemonSeed {
  return planted('s-stand1', 'merge the readiness check', {
    status: 'growing',
    ...memberClaim('trellis'),
    standby: {
      note: 'Merge once approved',
      set_at: SET_AT,
      set_by: USER_ACTOR,
      pull_requests: [watching('blocked')],
      ...overrides,
    },
    reminder: remindAtTonight(),
    ...seed,
  });
}

const growing = planted('s-grow11', 'ship the other thing', { status: 'growing', ...memberClaim('hazel') });
const waiting = planted('s-wait11', 'not started yet', { ready: true });

async function showBoard(seeds: DaemonSeed[]) {
  const opened = await openGarden(seeds);
  await gesture(opened.daemon, () => pressShortcut('board.open'));
  await gesture(opened.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'board' })));
  return opened;
}

async function replace(opened: Garden, seed: DaemonSeed, next: DaemonSeed) {
  opened.push(opened.seeds.map((candidate) => (candidate.id === seed.id ? next : candidate)));
  await opened.daemon.idle();
}

const theBoard = () => within(screen.getByRole('region', { name: 'The garden board' }));
const card = (title: string) => theBoard().getByRole('button', { name: new RegExp(`^${title}`) });

describe('App garden standby', () => {
  it('marks a standby card with its note, its pull request and its reminder, and follows the watch', async () => {
    const standing = onStandby();
    const opened = await showBoard([standing, growing]);

    expect(card('merge the readiness check')).toHaveTextContent('standby');
    expect(card('merge the readiness check')).toHaveTextContent('#123 blocked');
    expect(card('merge the readiness check')).toHaveTextContent('Merge once approved');
    expect(card('merge the readiness check')).toHaveTextContent(/reminder 23:30/);
    expect(card('ship the other thing')).not.toHaveTextContent('standby');

    await replace(opened, standing, onStandby({ pull_requests: [watching('ready')] }));

    expect(card('merge the readiness check')).toHaveTextContent('#123 ready');
    expect(card('merge the readiness check')).not.toHaveTextContent('#123 blocked');

    await replace(opened, standing, onStandby({ pull_requests: [watching('', 'merged')] }));

    expect(card('merge the readiness check')).toHaveTextContent('#123 merged');
  });

  it('drops the marker when standby ends', async () => {
    const standing = onStandby();
    const opened = await showBoard([standing]);
    expect(card('merge the readiness check')).toHaveTextContent('standby');

    const { standby: _ended, ...resumed } = standing;
    await replace(opened, standing, { ...resumed, rev: 2 });

    expect(card('merge the readiness check')).not.toHaveTextContent('standby');
    expect(card('merge the readiness check')).not.toHaveTextContent('Merge once approved');
    expect(card('merge the readiness check')).not.toHaveTextContent('#123');
  });

  it('says why a resume is blocked, and stops saying it once the reason is gone', async () => {
    const standing = onStandby();
    const opened = await showBoard([standing]);
    expect(card('merge the readiness check')).not.toHaveTextContent('resume blocked');

    const blocked = onStandby({ resume_blocked: 'worktree removed' });
    await replace(opened, standing, blocked);

    const blockedMarker = within(card('merge the readiness check')).getByText('resume blocked: worktree removed');
    expect(blockedMarker).toHaveAttribute('title', 'worktree removed');

    await replace(opened, blocked, standing);

    expect(card('merge the readiness check')).not.toHaveTextContent('resume blocked');
  });

  it('marks a standby row in the list, with its note, pull request and blocked resume in the tooltip', async () => {
    const standing = onStandby();
    const opened = await openGarden([standing, growing]);
    const row = (title: string) => within(gardenRegion()).getByRole('button', { name: new RegExp(`^${title}`) });

    const marker = within(row('merge the readiness check')).getByText('standby');
    expect(marker.getAttribute('title')).toContain('Merge once approved');
    expect(marker.getAttribute('title')).toContain('#123 blocked');
    expect(marker.getAttribute('title')).toMatch(/reminder 23:30/);
    expect(row('ship the other thing')).not.toHaveTextContent('standby');
    expect(row('ship the other thing')).toHaveTextContent('growing');

    const blocked = onStandby({ resume_blocked: 'worktree removed' });
    await replace(opened, standing, blocked);

    const blockedMarker = within(row('merge the readiness check')).getByText('standby · resume blocked');
    expect(blockedMarker.getAttribute('title')).toContain('resume blocked: worktree removed');

    const { standby: _ended, ...resumed } = blocked;
    await replace(opened, blocked, { ...resumed, rev: 2 });

    expect(row('merge the readiness check')).not.toHaveTextContent('standby');
    expect(row('merge the readiness check')).toHaveTextContent('growing');
  });

  it('keeps a standby row\'s dependency blockers beside its marker until the blocker closes', async () => {
    const standing = onStandby();
    const blocker = planted('s-block1', 'the prerequisite', { edges: [{ kind: 'blocks', to: standing.id }] });
    const blockedOther = planted('s-other1', 'waits its turn');
    const blockerOfOther = planted('s-block2', 'the other prerequisite', { edges: [{ kind: 'blocks', to: blockedOther.id }] });
    const quiet = { ...standing, id: 's-stand2', title: 'wait for the rollout' };
    const opened = await openGarden([blocker, standing, blockerOfOther, blockedOther, quiet]);
    const row = (title: string) => within(gardenRegion()).getByRole('button', { name: new RegExp(`^${title}`) });

    expect(within(row('merge the readiness check')).getByText('standby')).toBeVisible();
    expect(row('merge the readiness check')).toHaveTextContent('blocked by 1');
    expect(row('wait for the rollout')).toHaveTextContent('standby');
    expect(row('wait for the rollout')).not.toHaveTextContent('blocked by');
    expect(row('waits its turn')).toHaveTextContent('blocked by 1');
    expect(row('waits its turn')).not.toHaveTextContent('standby');

    const resumeBlocked = onStandby({ resume_blocked: 'worktree removed' });
    await replace(opened, standing, resumeBlocked);

    expect(within(row('merge the readiness check')).getByText('standby · resume blocked')).toBeVisible();
    expect(row('merge the readiness check')).toHaveTextContent('blocked by 1');

    await replace(opened, blocker, { ...blocker, status: 'harvested', rev: 2 });

    expect(row('merge the readiness check')).toHaveTextContent('standby · resume blocked');
    expect(row('merge the readiness check')).not.toHaveTextContent('blocked by');
  });

  it('keeps only the seeds on standby under the is:standby lens', async () => {
    const standing = onStandby();
    const opened = await openGarden([standing, growing, waiting]);
    const field = within(gardenRegion()).getByRole('combobox') as HTMLInputElement;
    const listed = () => within(gardenRegion()).queryAllByRole('listitem').map((item) => item.textContent ?? '').join('\n');
    expect(listed()).toContain('ship the other thing');

    fireEvent.change(field, { target: { value: 'is:standby' } });

    expect(listed()).toContain('merge the readiness check');
    expect(listed()).not.toContain('ship the other thing');
    expect(listed()).not.toContain('not started yet');
    expect(screen.queryByText(/no filter called/)).toBeNull();

    const { standby: _ended, ...resumed } = standing;
    await replace(opened, standing, { ...resumed, rev: 2 });

    expect(listed()).not.toContain('merge the readiness check');
  });
});
