
import { ShortcutId } from './registry';
import { modifierTokens, shortcutTokens } from './formatShortcut';
import { shortcutLabel } from './metadata';

export interface CheatsheetRow {
  label: string;
  combos: string[][];
  note?: string;
}

export interface CheatsheetCategory {
  title: string;
  rows: CheatsheetRow[];
}

function fromId(id: ShortcutId): string[] {
  return shortcutTokens(id);
}

export function buildCheatsheet({ shelfLayoutEnabled = false } = {}): CheatsheetCategory[] {
  return [
    {
      title: 'Desktops & Sessions',
      rows: [
        { label: 'New terminal on this desktop', combos: [fromId('session.newTerminal')] },
        { label: 'New session on this desktop', combos: [fromId('session.new')] },
        { label: shortcutLabel('session.newHorizontal', shelfLayoutEnabled), combos: [fromId('session.newHorizontal')] },
        { label: 'Close session, focused pane or empty desktop', combos: [fromId('session.close')] },
        {
          label: 'Previous / next desktop (queue mode: agent in the queue)',
          combos: [fromId('session.prev'), fromId('session.next')],
        },
        {
          label: 'Back / forward through agent history',
          combos: [fromId('session.historyBack'), fromId('session.historyForward')],
        },
        { label: 'Jump to this session\'s dispatcher', combos: [fromId('session.orchestrator')] },
        {
          label: 'Switch to desktop 1–9',
          combos: [[...modifierTokens('desktop.select1'), '1–9']],
          note: 'The digit of the current desktop goes back to the previous one.',
        },
        {
          label: 'Move this to desktop 1–9 and follow',
          combos: [[...modifierTokens('desktop.send1'), '1–9']],
          note: 'A digit with no desktop yet gets a new one.',
        },
        {
          label: 'Move this to desktop 1–9',
          combos: [[...modifierTokens('desktop.sendStay1'), '1–9']],
          note: 'A digit with no desktop yet gets a new one.',
        },
        { label: 'Desktop overview', combos: [fromId('desktop.overview')] },
        { label: 'Switch profile', combos: [fromId('profile.switch')] },
        { label: 'Go to dashboard (home)', combos: [fromId('session.goToDashboard')] },
        { label: 'Jump to next waiting session', combos: [fromId('session.jumpToWaiting')] },
        { label: 'Next automation run needing you', combos: [fromId('session.nextRun')] },
        { label: 'All agents', combos: [fromId('sidebar.agentList')] },
        { label: 'Settle turn', combos: [fromId('session.settle')] },
        { label: 'Asks drawer', combos: [fromId('dock.asks')] },
        { label: 'Toggle session priority', combos: [fromId('session.priority')] },
        { label: 'Snooze this agent', combos: [fromId('session.snooze')] },
        { label: 'Stop the countdown, or keep the next turn', combos: [fromId('session.cancelCountdown')] },
        { label: 'Toggle sidebar', combos: [fromId('session.toggleSidebar')] },
        {
          label: 'Sessions list, live and closed',
          combos: [fromId('sessions.open')],
          note: 'Reopen a closed session from here.',
        },
      ],
    },
    {
      title: 'Panes & Terminals',
      rows: [
        { label: 'Find in terminal', combos: [fromId('terminal.find')] },
        { label: shortcutLabel('terminal.splitVertical', shelfLayoutEnabled), combos: [fromId('terminal.splitVertical')] },
        { label: shortcutLabel('terminal.splitHorizontal', shelfLayoutEnabled), combos: [fromId('terminal.splitHorizontal')] },
        {
          label: 'Move focus between panes',
          combos: [[...modifierTokens('terminal.focusLeft'), '←↑→↓']],
          note: 'Steps to the next desktop at an edge.',
        },
        ...(shelfLayoutEnabled
          ? [
              { label: 'Carry tile along the row', combos: [fromId('shelf.carryLeft'), fromId('shelf.carryRight')] },
              { label: 'Send tile to the shelf', combos: [fromId('shelf.sendToShelf')], note: 'A held tile returns with a chip, ⌘J or a restart.' },
            ]
          : []),
        { label: 'Zoom active pane', combos: [fromId('terminal.toggleZoom')] },
        { label: 'Focus active pane', combos: [fromId('terminal.toggleMaximize')] },
        { label: 'Focus utility terminal', combos: [fromId('terminal.open')] },
        ...(shelfLayoutEnabled
          ? [{
              label: 'Fewer, wider / more, narrower tiles',
              combos: [fromId('shelf.fewerTiles'), fromId('shelf.moreTiles')],
            }]
          : []),
      ],
    },
    {
      title: 'Markdown & Annotations',
      rows: [
        {
          label: 'Send annotations to session',
          combos: [fromId('markdown.sendAnnotations')],
          note: 'When a markdown tile has focus and annotations exist.',
        },
        {
          label: 'Send terminal annotations to session',
          combos: [fromId('terminal.sendAnnotations')],
          note: 'When the annotated pane has focus and marks are waiting.',
        },
      ],
    },
    {
      title: 'Review & Git',
      rows: [
        { label: 'PRs drawer', combos: [fromId('dock.attention')] },
        { label: 'Refresh PRs', combos: [fromId('session.refreshPRs')] },
      ],
    },
    {
      title: 'App',
      rows: [
        { label: 'Agent palette', combos: [fromId('ui.actionMenu')] },
        { label: 'Command palette', combos: [fromId('ui.commandPalette')] },
        { label: 'Settings', combos: [fromId('ui.openSettings')] },
        {
          label: 'Font size up / down / reset',
          combos: [
            fromId('ui.increaseFontSize'),
            fromId('ui.decreaseFontSize'),
            fromId('ui.resetFontSize'),
          ],
        },
        { label: 'Keyboard shortcuts', combos: [fromId('ui.showShortcuts')] },
        { label: 'Quit attn', combos: [fromId('app.quit')] },
      ],
    },
  ];
}
