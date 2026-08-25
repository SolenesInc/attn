import { triggerShortcut, hasHandler } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';
import { matchesShortcut, ShortcutId, isChord } from '../../shortcuts/registry';
import { resolveBinding } from '../../shortcuts/resolver';
import { enterLeader, resolvePendingThen } from '../../shortcuts/chordState';
import { matchChordLeader } from '../../shortcuts/chordDispatch';

const TERMINAL_INTERCEPTS: ShortcutId[] = [
  'workspace.select1', 'workspace.select2', 'workspace.select3',
  'workspace.select4', 'workspace.select5', 'workspace.select6',
  'workspace.select7', 'workspace.select8', 'workspace.select9',
  'session.newWorkspace',
  'app.quit',
  'ui.showShortcuts',
  'session.newHorizontal',
  'terminal.find',
  'terminal.splitVertical',
  'terminal.splitHorizontal',
  'terminal.toggleZoom',
  'terminal.toggleMaximize',
];

function matchesBinding(event: KeyboardEvent, id: ShortcutId): boolean {
  const def = resolveBinding(id);
  return def && !isChord(def) ? matchesShortcut(event, def) : false;
}

export function createTerminalKeyInterceptor(sendToPty: (data: string) => void) {
  return (event: KeyboardEvent) => {
    // A pending leader owns the next keystroke; resolve it before any PTY
    // control-sequence handling so the follow key is never emitted as input.
    if (event.type === 'keydown') {
      const pendingThen = resolvePendingThen(event);
      if (pendingThen.kind !== 'none') {
        if (pendingThen.kind === 'fired') triggerShortcut(pendingThen.id);
        return true;
      }
    }

    if (
      event.type === 'keydown'
      && (event.key === 'Tab' || event.key === 'ISO_Left_Tab')
      && event.shiftKey
      && !event.ctrlKey
      && !event.metaKey
      && !event.altKey
    ) {
      sendToPty('\x1b[Z');
      return true;
    }
    if (
      event.type === 'keydown'
      && event.ctrlKey
      && !event.metaKey
      && !event.altKey
      && !event.shiftKey
      && event.key.toLowerCase() === 'v'
      && isMacLikePlatform()
    ) {
      // On macOS Ctrl+V is the agent image-paste trigger; elsewhere it is the normal
      // browser text-paste accelerator.
      sendToPty('\x16');
      return true;
    }

    if (event.type === 'keydown') {
      for (const id of TERMINAL_INTERCEPTS) {
        if (matchesBinding(event, id)) {
          return triggerShortcut(id);
        }
      }
      if (matchesBinding(event, 'terminal.close') && triggerShortcut('terminal.close')) {
        return true;
      }
      if (matchesBinding(event, 'session.close')) {
        return triggerShortcut('session.close');
      }
      const chord = matchChordLeader(event);
      if (chord) {
        const fireable = chord.candidates.filter((c) => hasHandler(c.id));
        if (fireable.length > 0) {
          enterLeader(chord.leader, fireable);
        }
        return true;
      }
    }

    if (event.key === 'Enter' && event.shiftKey && !event.ctrlKey && !event.altKey) {
      if (event.type === 'keydown') {
        sendToPty('\n');
      }
      return true;
    }
    return false;
  };
}
