import { describe, expect, it } from 'vitest';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../types/generated';
import { buildDesktopViewModels, UNPLACED_GROUP_ID } from './workspaceViewModels';

function desktop(
  id: string,
  slot: number | null,
  tree: unknown,
  panes: Array<{ pane_id: string; session_id: string; status?: LayoutPaneStatus }>,
): Desktop {
  return {
    id,
    profile_id: 'profile',
    name: '',
    ...(slot ? { shortcut_slot: slot } : {}),
    order_key: id,
    tree_json: tree ? JSON.stringify(tree) : '',
    active_pane_id: panes[0]?.pane_id ?? '',
    revision: 1,
    panes: panes.map((pane) => ({
      desktop_id: id,
      kind: LayoutPaneKind.Agent,
      title: pane.session_id,
      status: LayoutPaneStatus.Ready,
      ...pane,
    })),
  };
}

const session = (id: string) => ({ id, label: id });

describe('buildDesktopViewModels', () => {
  it('groups agents by the desktop holding their pane, slotted desktops first, then extras, then Unplaced', () => {
    const desktops = [
      desktop('extra', null, { type: 'pane', pane_id: 'p-c' }, [{ pane_id: 'p-c', session_id: 'c' }]),
      desktop('two', 2, { type: 'pane', pane_id: 'p-b' }, [{ pane_id: 'p-b', session_id: 'b' }]),
      desktop('one', 1, { type: 'pane', pane_id: 'p-a' }, [{ pane_id: 'p-a', session_id: 'a' }]),
      desktop('empty', 3, null, []),
    ];

    const groups = buildDesktopViewModels(desktops, ['a', 'b', 'c', 'd'].map(session));

    expect(groups.map((group) => [group.id, group.title, group.sessions.map((entry) => entry.id)])).toEqual([
      ['one', 'Desktop 1', ['a']],
      ['two', 'Desktop 2', ['b']],
      ['empty', 'Desktop 3', []],
      ['extra', 'Desktop 10', ['c']],
      [UNPLACED_GROUP_ID, 'Unplaced', ['d']],
    ]);
  });

  it('orders a desktop\'s children by its layout, tiles included', () => {
    const [group] = buildDesktopViewModels(
      [
        desktop(
          'one',
          1,
          {
            type: 'split',
            split_id: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'tile', tile_id: 'notes', tile_kind: 'markdown', tile_params: '/notes.md' },
              { type: 'pane', pane_id: 'p-a' },
            ],
          },
          [{ pane_id: 'p-a', session_id: 'a' }],
        ),
      ],
      [session('a')],
    );

    expect(group.children.map((child) => [child.kind, child.id])).toEqual([
      ['tile', 'notes'],
      ['session', 'a'],
    ]);
    expect(group.firstSessionId).toBe('a');
  });

  it('flags a spawning or failed pane whose agent is not live without inventing a session', () => {
    const [group] = buildDesktopViewModels(
      [
        desktop('one', 1, { type: 'pane', pane_id: 'p-gone' }, [
          { pane_id: 'p-gone', session_id: 'gone', status: LayoutPaneStatus.Failed },
        ]),
      ],
      [],
    );

    expect(group.hasUnresolvedAgentPanes).toBe(true);
    expect(group.sessions).toEqual([]);
  });
});
