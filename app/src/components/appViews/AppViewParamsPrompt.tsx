import { useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import './AppViewParamsPrompt.css';

interface AppViewParamsPromptProps {
  /** "reviewer/approvals" — which view is asking. */
  viewTitle: string;
  /** The app's own wording for the field. */
  label: string;
  placeholder?: string;
  onSubmit: (params: string) => void;
  onClose: () => void;
}

export function AppViewParamsPrompt({
  viewTitle,
  label,
  placeholder,
  onSubmit,
  onClose,
}: AppViewParamsPromptProps) {
  const [value, setValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEscapeStack(onClose, true);

  const submit = () => {
    // An empty answer is a legitimate one: the view is told it got none.
    onSubmit(value.trim());
    onClose();
  };

  return (
    <div className="app-view-params-prompt" role="presentation" onClick={onClose}>
      {/* The command menu's own trap returns focus to the terminal as it closes,
          after this mounts, so focus-on-mount alone loses every keystroke. */}
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
          initialFocus: () => inputRef.current ?? false,
        }}
      >
        <div
          className="app-view-params-content"
          role="dialog"
          aria-modal="true"
          aria-labelledby="app-view-params-title"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="app-view-params-title" id="app-view-params-title">{viewTitle}</div>
          <label className="app-view-params-label" htmlFor="app-view-params-input">{label}</label>
          <input
            ref={inputRef}
            id="app-view-params-input"
            className="app-view-params-input"
            data-testid="app-view-params-input"
            type="text"
            spellCheck={false}
            placeholder={placeholder}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                submit();
              }
            }}
          />
          <div className="app-view-params-actions">
            <button type="button" className="cancel" onClick={onClose}>Cancel</button>
            <button type="button" className="confirm" data-testid="app-view-params-dock" onClick={submit}>Dock</button>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
