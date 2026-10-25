import type { ReactNode, RefObject } from 'react';
import './GardenCompose.css';

export function GardenCompose({
  verb,
  target,
  prompt,
  required,
  busy,
  inputRef,
  onCommit,
  onCancel,
  children,
}: {
  verb: string;
  target: string;
  prompt: string;
  required: boolean;
  busy: boolean;
  inputRef: RefObject<HTMLInputElement | null>;
  onCommit: () => void;
  onCancel: () => void;
  children?: ReactNode;
}) {
  return (
    <div className="garden-compose">
      <span className="garden-compose__verb">{verb.toLowerCase()}</span>
      <span className="garden-compose__id">{target}</span>
      <input
        ref={inputRef}
        className="garden-compose__input"
        type="text"
        autoComplete="off"
        spellCheck={false}
        disabled={busy}
        placeholder={required ? prompt : `${prompt} — optional`}
        aria-label={`${verb} ${target}: ${prompt}`}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Enter') {
            event.preventDefault();
            onCommit();
          }
          if (event.key === 'Escape') {
            event.preventDefault();
            onCancel();
          }
        }}
      />
      <span className="garden-compose__keys">
        <kbd>⏎</kbd>
        <kbd>esc</kbd>
      </span>
      {children}
    </div>
  );
}
