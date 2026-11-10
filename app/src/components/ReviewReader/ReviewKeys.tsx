import { FRAME_KEYS, type Frame } from './frames';

const DOCUMENT_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'stop'],
  [['↵'], 'open the file'],
  [['e'], 'more lines'],
  [['z'], 'fill the tile'],
  [['/'], 'search'],
  [['?'], 'all keys'],
];

const WALKTHROUGH_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'stop'],
  [['z'], 'hide the script'],
  [['/'], 'search'],
  [['?'], 'all keys'],
];

const MAP_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'message'],
  [['↵'], 'open the file'],
  [['e'], 'more lines'],
  [['/'], 'search'],
  [['?'], 'all keys'],
];

const FILE_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'change'],
  [['z'], 'fill the tile'],
  [['/'], 'search'],
  [['⌃o'], 'back'],
  [['esc'], 'close'],
  [['?'], 'all keys'],
];

const MARK_KEYS: Array<[string[], string]> = [
  [['c'], 'note'],
  [['a'], 'ask'],
  [['⌘↵'], 'send'],
];

function Keys({ keys }: { keys: string[] }) {
  return <>{keys.map((key) => <kbd key={key}>{key}</kbd>)}</>;
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
  onFrame,
}: {
  frame: Frame;
  offered: readonly Frame[];
  fileOpen: boolean;
  sides: boolean;
  marking: boolean;
  onFrame: (frame: Frame) => void;
}) {
  const keys = keysFor(frame, fileOpen, sides);
  return (
    <div className="review-key-line" aria-label="Review keys">
      {offered.length > 1 ? (
        <span className="review-frames" role="tablist" aria-label="Frames">
          {FRAME_KEYS.filter(([, value]) => offered.includes(value)).map(([key, value, name]) => (
            <button key={value} type="button" role="tab" tabIndex={-1} aria-selected={frame === value} onClick={() => onFrame(value)}>
              <kbd>{key}</kbd>{name}
            </button>
          ))}
        </span>
      ) : null}
      {(marking ? [...keys.slice(0, 2), ...MARK_KEYS, ...keys.slice(2)] : keys).map(([keys, what]) => (
        <span key={what}><Keys keys={keys} /> {what}</span>
      ))}
    </div>
  );
}

const ALL_KEYS: Array<[string[], string]> = [
  [['1', '2', '3'], 'Brief: the document top to bottom. Walkthrough: the stops as a script beside the file. Map: the sequence diagram beside the code of each message. A frame the document cannot fill is left out.'],
  [['↑', '↓'], 'Move by line in code, by block in prose. In the file view, by line; PageUp and PageDown move a page.'],
  [['⇧↑', '⇧↓'], 'Select several lines of code, so a note or question covers them.'],
  [['c'], 'Write a note on the line, the selected lines or the paragraph. It waits for the send.'],
  [['a'], 'Ask the agent about the line, the selected lines or the paragraph. The question goes at once; the answer shows under the line.'],
  [['⌘↵'], 'Send the review: see the notes it carries, pick a verdict (a approve, r request changes, c comment), then ↵.'],
  [['←', '→'], 'The previous or next stop; in the Map, the previous or next message. In the file view opened from Brief or Map, the previous or next change.'],
  [['b'], 'In the Map, switch between the diagrams before and after the change, when the document has both.'],
  [['↵'], 'Open the file the cursor is on, at that line, with the stop’s lines marked.'],
  [['e'], 'Show 10 more lines on each side of the stop.'],
  [['z'], 'Fill the tile with the file. In the file view, switch between filling the tile and the side panel. In the Walkthrough, hide or show the script.'],
  [['/'], 'Search the repository: files by name, lines by text.'],
  [['⌃o'], 'Go back to the file you came from; from the first file, close the file view. The Walkthrough keeps its stop.'],
  [['esc'], 'Close the send, the search, this sheet, a selection, or the file view. In the Walkthrough, return to the stop.'],
  [['?'], 'Show or hide this sheet.'],
];

export function ReviewKeySheet() {
  return (
    <div className="review-key-sheet" role="dialog" aria-label="Review keys">
      <h3>Keys</h3>
      <p>Selecting text and typing still writes a comment, as in any markdown tile.</p>
      <dl>
        {ALL_KEYS.map(([keys, what]) => (
          <div key={what} className="review-key-sheet-row">
            <dt><Keys keys={keys} /></dt>
            <dd>{what}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
