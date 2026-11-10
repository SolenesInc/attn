const DOCUMENT_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'line'],
  [['←', '→'], 'stop'],
  [['↵'], 'open the file'],
  [['e'], 'more lines'],
  [['z'], 'fill the tile'],
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

function Keys({ keys }: { keys: string[] }) {
  return <>{keys.map((key) => <kbd key={key}>{key}</kbd>)}</>;
}

export function ReviewKeyLine({ fileOpen }: { fileOpen: boolean }) {
  return (
    <div className="review-key-line" aria-label="Review keys">
      {(fileOpen ? FILE_KEYS : DOCUMENT_KEYS).map(([keys, what]) => (
        <span key={what}><Keys keys={keys} /> {what}</span>
      ))}
    </div>
  );
}

const ALL_KEYS: Array<[string[], string]> = [
  [['↑', '↓'], 'Move by line in code, by block in prose. In the file view, by line; PageUp and PageDown move a page.'],
  [['←', '→'], 'The previous or next stop. In the file view, the previous or next change.'],
  [['↵'], 'Open the file the cursor is on, at that line, with the stop’s lines marked.'],
  [['e'], 'Show 10 more lines on each side of the stop.'],
  [['z'], 'Fill the tile with the file. In the file view, switch between filling the tile and the side panel.'],
  [['/'], 'Search the repository: files by name, lines by text.'],
  [['⌃o'], 'Go back to the file you came from; from the first file, close the file view.'],
  [['esc'], 'Close the search, this sheet, or the file view.'],
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
