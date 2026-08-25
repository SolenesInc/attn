import { useRef, useEffect, useCallback, useState, useLayoutEffect } from 'react';
import './PathInput.css';

interface PathInputProps {
  value: string;
  onChange: (value: string) => void;
  onTabComplete: (value: string) => void;
  onSelect: (path: string) => void;
  onSubmit: () => void;
  ghostText: string;
  completionValue?: string;
  hasSelectedSinceTab?: boolean;
  placeholder?: string;
  autoFocus?: boolean;
}

export function PathInput({
  value,
  onChange,
  onTabComplete,
  onSelect,
  onSubmit,
  ghostText,
  completionValue,
  hasSelectedSinceTab = true,
  placeholder = 'Type path (e.g., ~/projects)...',
  autoFocus = true,
}: PathInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const measureRef = useRef<HTMLSpanElement>(null);
  const [ghostOffset, setGhostOffset] = useState(0);

  useEffect(() => {
    if (autoFocus) {
      inputRef.current?.focus();
    }
  }, [autoFocus]);

  useLayoutEffect(() => {
    if (measureRef.current) {
      setGhostOffset(measureRef.current.offsetWidth);
    }
  }, [value]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Tab' && completionValue) {
      e.preventDefault();
      onTabComplete(completionValue);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const pathToSelect = (ghostText && ghostText.startsWith(value) && hasSelectedSinceTab)
        ? ghostText
        : (value || completionValue);
      if (pathToSelect) {
        onSelect(pathToSelect);
        onSubmit();
      }
    }
  }, [completionValue, ghostText, hasSelectedSinceTab, onChange, onSelect, onSubmit, onTabComplete, value]);

  const visibleGhost = ghostText.startsWith(value)
    ? ghostText.slice(value.length)
    : '';

  return (
    <div className="path-input-container">
      {/* Hidden span to measure typed text width */}
      <span ref={measureRef} className="path-input-measure" aria-hidden="true">
        {value}
      </span>
      <input
        ref={inputRef}
        type="text"
        className="path-input"
        data-testid="location-picker-path-input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
      />
      {visibleGhost && (
        <span
          className="path-ghost"
          style={{ left: 16 + ghostOffset }}
        >
          {visibleGhost}
        </span>
      )}
    </div>
  );
}
