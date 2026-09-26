import { describe, expect, it } from 'vitest';
import type { UISessionState } from '../types/sessionState';
import { automationRunGroups, nextRunNeedingYou, type AutomationRunSession, type RunBatchStep } from './automationRuns';

const NOW = Date.parse('2026-09-26T12:00:00Z');

function run(
  id: string,
  definition: 'docs' | 'review',
  state: UISessionState,
  extra: Partial<AutomationRunSession> = {},
): AutomationRunSession {
  return {
    id,
    state,
    automation: { definition_id: definition, definition_name: definition === 'docs' ? 'nightly docs' : 'pr reviewer' },
    ...extra,
  };
}

const owed = (openedAt: string) => ({ turnOwed: true, turnOpenedAt: openedAt });

describe('automationRunGroups', () => {
  it('orders runs needing you oldest first, then live runs, then finished runs newest first', () => {
    const groups = automationRunGroups(
      [
        {
          sessions: [
            run('finished-old', 'review', 'idle', { stateSince: '2026-09-25T08:00:00Z' }),
            run('asking-new', 'review', 'waiting_input', owed('2026-09-26T11:00:00Z')),
            run('live', 'review', 'working'),
            run('finished-new', 'review', 'idle', { stateSince: '2026-09-26T10:00:00Z' }),
            run('asking-old', 'review', 'waiting_input', owed('2026-09-26T09:00:00Z')),
            { id: 'manual', state: 'working' },
          ],
        },
      ],
      NOW,
    );

    expect(groups).toHaveLength(1);
    expect(groups[0].runs.map((r) => r.id)).toEqual([
      'asking-old',
      'asking-new',
      'live',
      'finished-new',
      'finished-old',
    ]);
    expect(groups[0].needingYou.map((r) => r.id)).toEqual(['asking-old', 'asking-new']);
  });

  it('does not count a snoozed run as needing you, and lists a run on two desktops once', () => {
    const snoozed = run('snoozed', 'docs', 'waiting_input', {
      ...owed('2026-09-26T09:00:00Z'),
      turnSnoozedUntil: '2026-09-26T13:00:00Z',
    });
    const asking = run('asking', 'docs', 'waiting_input', owed('2026-09-26T10:00:00Z'));
    const groups = automationRunGroups([{ sessions: [snoozed, asking] }, { sessions: [asking] }], NOW);

    expect(groups[0].runs.map((r) => r.id)).toEqual(['asking', 'snoozed']);
    expect(groups[0].needingYou.map((r) => r.id)).toEqual(['asking']);
  });
});

describe('nextRunNeedingYou', () => {
  const groups = automationRunGroups(
    [
      {
        sessions: [
          run('review-1', 'review', 'waiting_input', owed('2026-09-26T08:00:00Z')),
          run('docs-1', 'docs', 'waiting_input', owed('2026-09-26T10:00:00Z')),
          run('docs-2', 'docs', 'waiting_input', owed('2026-09-26T11:00:00Z')),
          run('docs-live', 'docs', 'working'),
        ],
      },
    ],
    NOW,
  );

  it('walks the runs needing you in group order and wraps around', () => {
    const walk: string[] = [];
    let current: string | null = null;
    for (let press = 0; press < 4; press += 1) {
      const step: RunBatchStep<AutomationRunSession> | null = nextRunNeedingYou(groups, current);
      walk.push(`${step?.run.id} ${step?.position}/${step?.total}`);
      current = step?.run.id ?? null;
    }
    expect(walk).toEqual(['docs-1 1/3', 'docs-2 2/3', 'review-1 3/3', 'docs-1 1/3']);
  });

  it('starts from the first run when the one being looked at no longer needs you', () => {
    expect(nextRunNeedingYou(groups, 'docs-live')?.run.id).toBe('docs-1');
  });

  it('finds nothing when no run needs you', () => {
    expect(nextRunNeedingYou(automationRunGroups([{ sessions: [run('a', 'docs', 'idle')] }], NOW), null)).toBeNull();
  });
});
