import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import './Palette.css';

export interface PaletteProps<T> {
  variant: string;
  ariaLabel: string;
  placeholder: string;
  query: string;
  onQueryChange: (query: string) => void;
  items: T[];
  itemKey: (item: T) => string;
  renderItem: (item: T) => ReactNode;
  emptyLabel: string;
  onPick: (item: T) => void;
  onClose: () => void;
  onKeyDown?: (event: KeyboardEvent<HTMLInputElement>) => boolean;
}

export function Palette<T>({
  variant,
  ariaLabel,
  placeholder,
  query,
  onQueryChange,
  items,
  itemKey,
  renderItem,
  emptyLabel,
  onPick,
  onClose,
  onKeyDown,
}: PaletteProps<T>) {
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  const activeIndex = items.length === 0 ? -1 : Math.min(selected, items.length - 1);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    setSelected(0);
  }, [query]);

  useEffect(() => {
    if (activeIndex < 0) return;
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const pick = (item: T | undefined) => {
    if (item !== undefined) onPick(item);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (onKeyDown?.(event)) return;
    switch (event.key) {
      case 'Escape':
        // Closing the palette must not also bubble to a workspace-level Escape handler.
        event.preventDefault();
        event.stopPropagation();
        onClose();
        break;
      case 'ArrowDown':
        event.preventDefault();
        setSelected((i) => Math.min(i + 1, items.length - 1));
        break;
      case 'ArrowUp':
        event.preventDefault();
        setSelected((i) => Math.max(i - 1, 0));
        break;
      case 'Enter':
        event.preventDefault();
        pick(items[activeIndex]);
        break;
      default:
        break;
    }
  };

  const listId = `${variant}-list`;

  return (
    <div
      className={`palette ${variant}`}
      role="dialog"
      aria-label={ariaLabel}
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className={`palette-box ${variant}-box`}>
        <input
          ref={inputRef}
          className={`palette-input ${variant}-input`}
          type="text"
          placeholder={placeholder}
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
          onKeyDown={handleKeyDown}
          role="combobox"
          aria-expanded
          aria-controls={listId}
          aria-activedescendant={activeIndex >= 0 ? `${variant}-opt-${activeIndex}` : undefined}
          spellCheck={false}
          autoComplete="off"
        />
        <ul id={listId} ref={listRef} className={`palette-list ${variant}-list`} role="listbox">
          {items.length === 0 ? (
            <li className={`palette-empty ${variant}-empty`}>{emptyLabel}</li>
          ) : (
            items.map((item, index) => (
              <li
                key={itemKey(item)}
                id={`${variant}-opt-${index}`}
                role="option"
                aria-selected={index === activeIndex}
                className={`palette-option ${variant}-option${index === activeIndex ? ' is-selected' : ''}`}
                onMouseEnter={() => setSelected(index)}
                // mousedown + preventDefault: pick without yanking focus out of the input.
                onMouseDown={(event) => { event.preventDefault(); pick(item); }}
              >
                {renderItem(item)}
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}
