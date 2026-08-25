// app/src/components/Keycap.tsx
// Keycap rendering shared by the shortcuts cheatsheet and the what's-new modal.

import './Keycap.css';

export function KeyCombo({ tokens }: { tokens: string[] }) {
  return (
    <span className="key-combo">
      {tokens.map((token, i) => (
        token === 'then'
          ? <span className="key-combo-then" key={`then-${i}`}>then</span>
          : <kbd className="keycap" key={`${token}-${i}`}>{token}</kbd>
      ))}
    </span>
  );
}

/** Multiple combos rendered with a "/" separator (e.g. ⌘↑ / ⌘↓). */
export function KeyCombos({ combos }: { combos: string[][] }) {
  return (
    <span className="key-combos">
      {combos.map((combo, i) => (
        <span className="key-combos-item" key={i}>
          {i > 0 && <span className="key-combos-sep">/</span>}
          <KeyCombo tokens={combo} />
        </span>
      ))}
    </span>
  );
}
