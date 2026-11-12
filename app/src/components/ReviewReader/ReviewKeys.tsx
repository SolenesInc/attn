import { useEffect, useState } from 'react';
import type { ReviewNavigation } from '../../types/generated';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { FRAME_KEYS, type Frame } from './frames';

const DOCUMENT_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'stop'],
  [['↵'], 'open the file'],
  [['e'], 'more lines'],
  [['z'], 'fill the tile'],
  [['/'], 'search'],
];

const WALKTHROUGH_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'stop'],
  [['⇧←', '⇧→'], 'sideways'],
  [['z'], 'hide the script'],
  [['/'], 'search'],
];

const MAP_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'message'],
  [['↵'], 'open the file'],
  [['e'], 'more lines'],
  [['/'], 'search'],
];

const FILE_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'change'],
  [['⇧←', '⇧→'], 'sideways'],
  [['z'], 'fill the tile'],
  [['/'], 'search'],
  [['⌃o'], 'back'],
  [['esc'], 'close'],
];

const NAV_KEYS: Array<[string[], string]> = [
  [['f', 'r', 'h'], 'definition, uses, type'],
];

function sendKey(): string {
  return formatShortcut('markdown.sendAnnotations');
}

function markKeys(): Array<[string[], string]> {
  return [
    [['c'], 'note'],
    [['a'], 'ask'],
    [['i'], 'edit'],
    [[sendKey()], 'send'],
  ];
}

function Keys({ keys }: { keys: string[] }) {
  return <>{keys.map((key) => <kbd key={key}>{key}</kbd>)}</>;
}

const STATE_TEXT: Record<string, string> = {
  ready: 'ready',
  starting: 'loading',
  missing: 'no server',
  failed: 'failed',
};

/** What the reader needs to offer a language server's setup, from the tile that shows it. */
export interface ReviewSetup {
  /** Starts an agent that sets up the language's server; resolves to why it could not, or null. */
  start(navigation: ReviewNavigation): Promise<string | null>;
  /** The session's label while it works on the setup; undefined once it is gone or waits. */
  runningSessionLabel(sessionId: string): string | undefined;
  openSession(sessionId: string): void;
  /** False once the user said not to offer setup in this repository. */
  offered: boolean;
  setOffered(offered: boolean): void;
}

function hasNoServer(navigation: ReviewNavigation): boolean {
  return navigation.state === 'missing' || navigation.state === 'failed';
}

function runningSetup(navigation: ReviewNavigation, setup: ReviewSetup | undefined): string | undefined {
  const session = navigation.setup_session_id;
  return session && hasNoServer(navigation) ? setup?.runningSessionLabel(session) : undefined;
}

/** The first language with no working server and no setup running: the one s sets up. */
export function languageToSetUp(navigation: readonly ReviewNavigation[], setup: ReviewSetup | undefined): ReviewNavigation | undefined {
  if (!setup) return undefined;
  return navigation.find((entry) => hasNoServer(entry) && runningSetup(entry, setup) === undefined);
}

/** Now, to the second, while any server is starting; nothing ticks otherwise. */
function useNowWhileStarting(navigation: readonly ReviewNavigation[]): number {
  const starting = navigation.some((entry) => entry.started_at !== undefined);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!starting) return undefined;
    setNow(Date.now());
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [starting]);
  return now;
}

function startingFor(startedAt: string | undefined, now: number): string {
  const began = startedAt === undefined ? NaN : Date.parse(startedAt);
  return Number.isNaN(began) ? '' : ` ${Math.max(0, Math.floor((now - began) / 1000))}s`;
}

/** Each language's server, as the review's navigation reports it; the detail says what it is doing or why it is not there. */
function NavigationStates({ navigation, setup }: { navigation: readonly ReviewNavigation[]; setup?: ReviewSetup }) {
  const offer = setup?.offered ? languageToSetUp(navigation, setup) : undefined;
  const now = useNowWhileStarting(navigation);
  return (
    <span className="review-nav-states" aria-label="Language servers">
      {navigation.map((entry) => {
        const { language, state, detail } = entry;
        const setupLabel = runningSetup(entry, setup);
        if (setupLabel !== undefined && setup && entry.setup_session_id) {
          const session = entry.setup_session_id;
          return (
            <button key={language} type="button" tabIndex={-1} className="review-nav-state review-nav-state--setup" title={detail} onClick={() => setup.openSession(session)}>
              {language} setting up in {setupLabel}
            </button>
          );
        }
        return (
          <span key={language} className="review-nav-state-entry">
            <span className={`review-nav-state review-nav-state--${state}`} title={detail}>
              {language} {STATE_TEXT[state] ?? state}{state === 'starting' ? startingFor(entry.started_at, now) : ''}{(state === 'starting' || state === 'failed') && detail ? `: ${detail}` : ''}
            </span>
            {offer === entry ? (
              <>
                <span><Keys keys={['s']} /> set up</span>
                <span><Keys keys={['x']} /> not here</span>
              </>
            ) : null}
          </span>
        );
      })}
    </span>
  );
}

function keysFor(frame: Frame, fileOpen: boolean, sides: boolean): Array<[string[], string]> {
  if (fileOpen) return FILE_KEYS;
  if (frame === 'walkthrough') return WALKTHROUGH_KEYS;
  if (frame === 'map') return sides ? [...MAP_KEYS.slice(0, 4), [['b'], 'before, after'], ...MAP_KEYS.slice(4)] : MAP_KEYS;
  return DOCUMENT_KEYS;
}

export function ReviewKeyLine({
  frame,
  offered,
  fileOpen,
  sides,
  marking,
  navigation,
  setup,
  onFrame,
}: {
  frame: Frame;
  offered: readonly Frame[];
  fileOpen: boolean;
  sides: boolean;
  marking: boolean;
  navigation: readonly ReviewNavigation[];
  setup?: ReviewSetup;
  onFrame: (frame: Frame) => void;
}) {
  const keys = [...keysFor(frame, fileOpen, sides).slice(0, 2), ...NAV_KEYS, ...keysFor(frame, fileOpen, sides).slice(2)];
  return (
    <div className="review-key-line" aria-label="Review keys">
      <span className="review-key-line-entries">
        {offered.length > 1 ? (
          <span className="review-frames" role="tablist" aria-label="Frames">
            {FRAME_KEYS.filter(([, value]) => offered.includes(value)).map(([key, value, name]) => (
              <button key={value} type="button" role="tab" tabIndex={-1} aria-selected={frame === value} onClick={() => onFrame(value)}>
                <kbd>{key}</kbd>{name}
              </button>
            ))}
          </span>
        ) : null}
        {navigation.length > 0 ? <NavigationStates navigation={navigation} setup={setup} /> : null}
        {(marking ? [...keys.slice(0, 2), ...markKeys(), ...keys.slice(2)] : keys).map(([keys, what]) => (
          <span key={what}><Keys keys={keys} /> {what}</span>
        ))}
      </span>
      <span className="review-key-line-all"><Keys keys={['?']} /> all keys</span>
    </div>
  );
}

const allKeys = (): Array<[string[], string]> => [
  [['1', '2', '3'], 'Brief: the document top to bottom. Walkthrough: the stops as a script beside the file. Map: the sequence diagram beside the code of each message. A frame the document cannot fill is left out.'],
  [['↑', '↓'], 'Move by line in code, by block in prose. In the file view, by line; PageUp and PageDown move a page.'],
  [['⇧↑', '⇧↓'], 'Select several lines of code, so a note or question covers them.'],
  [['c'], 'Write a note on the line, the selected lines or the paragraph. It waits for the send.'],
  [['a'], 'Ask the agent about the line, the selected lines or the paragraph. The question goes at once; the answer shows under the line.'],
  [['i'], 'Edit what the cursor is on: the selected lines, else a whole string or comment of several lines, else the line; in a document, the paragraph or block as markdown. ⌥↑ and ⌥↓ take in a line. Saving writes the file and lists the edit in the review.'],
  [['u'], 'Undo your edit where the cursor is: the file gets its old text back and the edit leaves the review.'],
  [[sendKey()], 'Send the review: see the notes and edits it carries, pick a verdict (a approve, r request changes, c comment), then ↵.'],
  [['←', '→'], 'The previous or next stop; in the Map, the previous or next message. In the file view opened from Brief or Map, the previous or next change.'],
  [['⇧←', '⇧→'], 'Scroll the file sideways by half its width, in the file view and in the Walkthrough, to read the rest of a long line. A long line wraps in a stop.'],
  [['b'], 'In the Map, switch between the diagrams before and after the change, when the document has both.'],
  [['f'], 'Go to the definition of a name on the line: each name gets a label, type it. On a definition, list its uses. An answer that comes after you moved on is offered in one line above the keys: ↵ takes it, esc drops it, and every other key keeps working. The line asked about is marked … until the answer comes.'],
  [['r'], 'List the uses of a name on the line; ↑↓ pick one, ↵ opens it.'],
  [['h'], 'Show the type of a name on the line.'],
  [['s'], 'When a language of the review has no working server, start an agent on the review’s machine that sets one up. The review gains navigation once it works.'],
  [['x'], 'Stop offering setup in this repository, or offer it again. s still sets up.'],
  [['↵'], 'Open the file the cursor is on, at that line, with the stop’s lines marked. On the change’s facts under the title, show or hide the changed files.'],
  [['e'], 'Show 10 more lines on each side of the stop. The stop’s label names the lines it shows.'],
  [['z'], 'Fill the tile with the file. In the file view, switch between filling the tile and the side panel. In the Walkthrough, hide or show the script.'],
  [['/'], 'Search the repository: files by name, lines by text. The empty box lists the changed files with their added and removed lines.'],
  [['⌃o'], 'Go back to the file you came from; from the first file, close the file view. The Walkthrough keeps its stop.'],
  [['esc'], 'Close the edit box, the send, the search, this sheet, an answer or an offered one, a selection, or the file view. In the Walkthrough, return to the stop.'],
  [['?'], 'Show or hide this sheet.'],
];

export function ReviewKeySheet({ readOnly }: { readOnly: boolean }) {
  return (
    <div className="review-key-sheet" role="dialog" aria-label="Review keys">
      <h3>Keys</h3>
      <p>Selecting text and typing still writes a comment, as in any markdown tile.</p>
      {readOnly ? <p>This file is outside the repository and read-only: c, a and i do nothing here.</p> : null}
      <dl>
        {allKeys().map(([keys, what]) => (
          <div key={what} className="review-key-sheet-row">
            <dt><Keys keys={keys} /></dt>
            <dd>{what}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
