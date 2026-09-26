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
  renderItem: (item: T, highlighted: boolean) => ReactNode;
  isSelectable?: (item: T) => boolean;
  emptyLabel: string;
  onPick: (item: T) => void;
  onClose: () => void;
  onKeyDown?: (event: KeyboardEvent<HTMLInputElement>, highlighted: T | undefined) => boolean;
  inputPrefix?: ReactNode;
  inputSuffix?: ReactNode;
  footer?: ReactNode;
}

function selectableStep<T>(items: T[], from: number, direction: 1 | -1, isSelectable: (item: T) => boolean): number {
  for (let index = from + direction; index >= 0 && index < items.length; index += direction) {
    if (isSelectable(items[index])) return index;
  }
  return from;
}

const everyItem = () => true;

export function Palette<T>({
  variant,
  ariaLabel,
  placeholder,
  query,
  onQueryChange,
  items,
  itemKey,
  renderItem,
  isSelectable = everyItem,
  emptyLabel,
  onPick,
  onClose,
  onKeyDown,
  inputPrefix,
  inputSuffix,
  footer,
}: PaletteProps<T>) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  const selectedIndex = selectedKey === null ? -1 : items.findIndex((item) => itemKey(item) === selectedKey);
  const activeIndex = selectedIndex >= 0 && isSelectable(items[selectedIndex])
    ? selectedIndex
    : items.findIndex(isSelectable);
  const highlighted = activeIndex >= 0 ? items[activeIndex] : undefined;
  const selectIndex = (index: number) => setSelectedKey(index >= 0 ? itemKey(items[index]) : null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    setSelectedKey(null);
  }, [query]);

  useEffect(() => {
    if (activeIndex < 0) return;
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const pick = (item: T | undefined) => {
    if (item !== undefined && isSelectable(item)) onPick(item);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (onKeyDown?.(event, highlighted)) return;
    switch (event.key) {
      case 'Escape':
        // Closing the palette must not also bubble to a workspace-level Escape handler.
        event.preventDefault();
        event.stopPropagation();
        onClose();
        break;
      case 'ArrowDown':
        event.preventDefault();
        selectIndex(selectableStep(items, activeIndex, 1, isSelectable));
        break;
      case 'ArrowUp':
        event.preventDefault();
        selectIndex(selectableStep(items, activeIndex, -1, isSelectable));
        break;
      case 'Enter':
        event.preventDefault();
        pick(highlighted);
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
        <div className={`palette-input-row ${variant}-input-row`}>
          {inputPrefix}
          <input
            ref={inputRef}
            className={`palette-input ${variant}-input`}
            type="text"
            placeholder={placeholder}
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            onKeyDown={handleKeyDown}
            role="combobox"
            aria-label={ariaLabel}
            aria-expanded
            aria-controls={listId}
            aria-activedescendant={activeIndex >= 0 ? `${variant}-opt-${activeIndex}` : undefined}
            spellCheck={false}
            autoComplete="off"
          />
          {inputSuffix}
        </div>
        <ul id={listId} ref={listRef} className={`palette-list ${variant}-list`} role="listbox">
          {items.length === 0 ? (
            <li className={`palette-empty ${variant}-empty`}>{emptyLabel}</li>
          ) : (
            items.map((item, index) =>
              isSelectable(item) ? (
                <li
                  key={itemKey(item)}
                  id={`${variant}-opt-${index}`}
                  role="option"
                  aria-selected={index === activeIndex}
                  className={`palette-option ${variant}-option${index === activeIndex ? ' is-selected' : ''}`}
                  onMouseEnter={() => selectIndex(index)}
                  // mousedown + preventDefault: pick without yanking focus out of the input.
                  onMouseDown={(event) => { event.preventDefault(); pick(item); }}
                >
                  {renderItem(item, index === activeIndex)}
                </li>
              ) : (
                <li
                  key={itemKey(item)}
                  role="presentation"
                  className={`palette-heading ${variant}-heading`}
                  onMouseDown={(event) => event.preventDefault()}
                >
                  {renderItem(item, false)}
                </li>
              ),
            )
          )}
        </ul>
        {footer && <div className={`palette-footer ${variant}-footer`}>{footer}</div>}
      </div>
    </div>
  );
}
