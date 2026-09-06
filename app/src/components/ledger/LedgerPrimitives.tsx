import { useCallback, useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, ReactNode } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';

export type RowGlyph =
  | 'live' | 'working' | 'waiting' | 'closed' | 'refreshing'
  | 'pinned' | 'scheduled' | 'dirty' | 'clean' | 'removed' | 'error';

export interface RowVerb {
  id: string;
  label: string;
  danger?: boolean;
}

export interface RowNote {
  kind: 'busy' | 'refused' | 'info';
  text: string;
}

export interface RowModel {
  key: string;
  glyph: RowGlyph;
  title: string;
  titleHint?: string;
  /** Secondary line segments; rendered with a dot between each. */
  meta: ReactNode[];
  /** A short trailing word on the right of the meta line, e.g. the relative time. */
  stamp?: { text: string; hint?: string };
  note?: RowNote;
  verbs: RowVerb[];
  dim?: boolean;
  /** Text `y` copies: the row's path. */
  yank?: string;
  /** Facts the automation bridge reads off the row as `data-*`, so it never parses prose. */
  attrs?: Record<string, string>;
}

export type ListItem =
  | { kind: 'group'; key: string; title: string; meta?: ReactNode }
  | { kind: 'row'; row: RowModel };

interface LedgerListProps {
  items: ListItem[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  onVerb: (key: string, verbId: string) => void;
  onEnter?: (key: string) => void;
  menuKey: string | null;
  onMenu: (key: string | null) => void;
  onYank?: (text: string) => void;
  empty?: ReactNode;
}

export function LedgerList({
  items, selectedKey, onSelect, onVerb, onEnter, menuKey, onMenu, onYank, empty,
}: LedgerListProps) {
  const listRef = useRef<HTMLDivElement>(null);
  const rows = items.filter((item): item is Extract<ListItem, { kind: 'row' }> => item.kind === 'row');

  const focusRow = useCallback((key: string) => {
    const node = listRef.current?.querySelector<HTMLElement>(`[data-row-key="${CSS.escape(key)}"]`);
    node?.focus({ preventScroll: true });
    node?.scrollIntoView?.({ block: 'nearest' });
  }, []);

  // An open menu is the top Escape layer; it closes before the surface does.
  useEscapeStack(() => onMenu(null), menuKey !== null);

  // Rows arrive after the surface opens; land on one unless the user is already typing in the panel.
  const hadRows = useRef(false);
  useEffect(() => {
    const has = rows.length > 0;
    if (has && !hadRows.current) {
      const panel = listRef.current?.closest('.ledger-panel');
      if (panel && !panel.contains(document.activeElement)) focusRow(selectedKey ?? rows[0].row.key);
    }
    hadRows.current = has;
  });

  const move = useCallback((offset: number) => {
    if (rows.length === 0) return;
    const current = Math.max(0, rows.findIndex((item) => item.row.key === selectedKey));
    const next = Math.min(rows.length - 1, Math.max(0, current + offset));
    onSelect(rows[next].row.key);
    onMenu(null);
    focusRow(rows[next].row.key);
  }, [rows, selectedKey, onSelect, onMenu, focusRow]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>, row: RowModel) => {
    const busy = row.note?.kind === 'busy';
    if (event.key === 'ArrowDown' || event.key === 'j') { event.preventDefault(); move(1); return; }
    if (event.key === 'ArrowUp' || event.key === 'k') { event.preventDefault(); move(-1); return; }
    if (event.key === 'Home') { event.preventDefault(); move(-rows.length); return; }
    if (event.key === 'End') { event.preventDefault(); move(rows.length); return; }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (onEnter) onEnter(row.key);
      else if (!busy && row.verbs[0]) onVerb(row.key, row.verbs[0].id);
      return;
    }
    if (event.key === '.' || (event.key === 'ArrowRight' && row.verbs.length > 1)) {
      if (row.verbs.length > 1) { event.preventDefault(); onMenu(menuKey === row.key ? null : row.key); }
      return;
    }
    if (event.key === 'ArrowLeft' && menuKey === row.key) { event.preventDefault(); onMenu(null); return; }
    if (/^[1-9]$/.test(event.key)) {
      const verb = row.verbs[Number(event.key) - 1];
      if (verb && !busy) { event.preventDefault(); onVerb(row.key, verb.id); }
      return;
    }
    if (event.key === 'y' && row.yank && onYank) { event.preventDefault(); onYank(row.yank); }
  }, [move, rows.length, onEnter, onVerb, onMenu, menuKey, onYank]);

  return (
    <div className="ledger-list" ref={listRef} role="listbox" aria-label="Rows">
      {items.length === 0 && empty}
      {items.map((item) => item.kind === 'group'
        ? (
          <div className="ledger-group" key={`group:${item.key}`} role="presentation">
            <span className="ledger-group-title">{item.title}</span>
            {item.meta && <span className="ledger-group-meta">{item.meta}</span>}
          </div>
        )
        : (
          <LedgerRow
            key={item.row.key}
            row={item.row}
            selected={item.row.key === selectedKey}
            menuOpen={menuKey === item.row.key}
            onSelect={() => onSelect(item.row.key)}
            onKeyDown={(event) => onKeyDown(event, item.row)}
            onVerb={(verbId) => onVerb(item.row.key, verbId)}
            onToggleMenu={() => onMenu(menuKey === item.row.key ? null : item.row.key)}
          />
        ))}
    </div>
  );
}

interface LedgerRowProps {
  row: RowModel;
  selected: boolean;
  menuOpen: boolean;
  onSelect: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
  onVerb: (verbId: string) => void;
  onToggleMenu: () => void;
}

function LedgerRow({ row, selected, menuOpen, onSelect, onKeyDown, onVerb, onToggleMenu }: LedgerRowProps) {
  const busy = row.note?.kind === 'busy';
  const primary = row.verbs[0];
  const className = [
    'ledger-row',
    selected ? 'is-selected' : '',
    row.dim ? 'is-dim' : '',
    busy ? 'is-busy' : '',
  ].filter(Boolean).join(' ');
  const dataAttrs = Object.fromEntries(Object.entries(row.attrs ?? {}).map(([key, value]) => [`data-${key}`, value]));
  return (
    <div
      className={className}
      role="option"
      tabIndex={0}
      data-row-key={row.key}
      {...dataAttrs}
      aria-selected={selected}
      aria-busy={busy || undefined}
      onFocus={onSelect}
      onClick={onSelect}
      onKeyDown={onKeyDown}
    >
      <span className={`ledger-glyph is-${row.glyph}`} aria-hidden="true" />
      <div className="ledger-row-body">
        <div className="ledger-row-title" title={row.titleHint}>{row.title}</div>
        <div className="ledger-row-meta">
          {row.meta.filter((segment) => segment !== null && segment !== undefined && segment !== '')
            .map((segment, index) => (
              <span className="ledger-meta-seg" key={index}>{segment}</span>
            ))}
        </div>
        {row.note && row.note.kind !== 'busy' && (
          <div className={`ledger-row-note is-${row.note.kind}`} role="status" title={row.note.text}>
            {row.note.text}
          </div>
        )}
      </div>
      <div className="ledger-row-trailing">
        {row.stamp && <span className="ledger-stamp" title={row.stamp.hint}>{row.stamp.text}</span>}
        {primary && (
          <button
            type="button"
            className={`ledger-verb${primary.danger ? ' is-danger' : ''}`}
            disabled={busy}
            onClick={(event) => { event.stopPropagation(); onVerb(primary.id); }}
          >
            {busy ? row.note?.text : primary.label}
          </button>
        )}
        {row.verbs.length > 1 && (
          <span className="ledger-menu-anchor">
            <button
              type="button"
              className="ledger-more"
              aria-label={`More for ${row.title}`}
              aria-expanded={menuOpen}
              disabled={busy}
              onClick={(event) => { event.stopPropagation(); onToggleMenu(); }}
            >
              ···
            </button>
            {menuOpen && (
              <ul className="ledger-menu" role="menu">
                {row.verbs.map((verb, index) => (
                  <li key={verb.id} role="none">
                    <button
                      type="button"
                      role="menuitem"
                      className={verb.danger ? 'is-danger' : undefined}
                      onClick={(event) => { event.stopPropagation(); onVerb(verb.id); }}
                    >
                      <kbd>{index + 1}</kbd>{verb.label}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </span>
        )}
      </div>
    </div>
  );
}

export interface Segment<T extends string> {
  id: T;
  label: string;
}

export function Segmented<T extends string>({
  value, options, onChange, label,
}: { value: T; options: Segment<T>[]; onChange: (id: T) => void; label: string }) {
  return (
    <div className="ledger-segmented" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.id}
          type="button"
          className={option.id === value ? 'is-selected' : undefined}
          aria-pressed={option.id === value}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

export interface Chip {
  text: string;
  tone?: 'unresolved';
  onRemove: () => void;
}

export function QueryBar({
  value, onChange, placeholder, chips, inputRef,
}: {
  value: string;
  onChange: (text: string) => void;
  placeholder: string;
  chips: Chip[];
  inputRef: React.RefObject<HTMLInputElement | null>;
}) {
  const [focused, setFocused] = useState(false);
  // Escape peels one layer: text first, then the input itself, handing focus back to the rows.
  useEscapeStack(() => {
    if (value) { onChange(''); return; }
    inputRef.current?.closest('.ledger-panel')?.querySelector<HTMLElement>('.ledger-row.is-selected, .ledger-row')?.focus();
  }, focused);
  return (
    <div className="ledger-query">
      <span className="ledger-query-slash" aria-hidden="true">/</span>
      <input
        ref={inputRef}
        type="text"
        spellCheck={false}
        autoCorrect="off"
        autoCapitalize="off"
        value={value}
        placeholder={placeholder}
        aria-label="Filter"
        onChange={(event) => onChange(event.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
      />
      {chips.length > 0 && (
        <div className="ledger-chips">
          {chips.map((chip) => (
            <button
              key={chip.text}
              type="button"
              className={`ledger-chip${chip.tone ? ` is-${chip.tone}` : ''}`}
              title={chip.tone === 'unresolved' ? 'Nothing matches this token' : 'Remove'}
              onClick={chip.onRemove}
            >
              {chip.text}<span aria-hidden="true">×</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function Inspector({ title, kicker, children }: { title: string; kicker?: ReactNode; children: ReactNode }) {
  return (
    <aside className="ledger-inspector" aria-label="Details">
      <div className="ledger-inspector-head">
        <div className="ledger-inspector-title" title={title}>{title}</div>
        {kicker && <div className="ledger-inspector-kicker">{kicker}</div>}
      </div>
      <div className="ledger-inspector-body">{children}</div>
    </aside>
  );
}

export function Field({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="ledger-field">
      <div className="ledger-field-label">{label}</div>
      <div className={`ledger-field-value${mono ? ' is-mono' : ''}`}>{children}</div>
    </div>
  );
}

/** A copy affordance that says "copied" for a beat and then forgets. */
export function useCopied(): [string | null, (text: string) => void] {
  const [copied, setCopied] = useState<string | null>(null);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(null), 1200);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = useCallback((text: string) => {
    void navigator.clipboard?.writeText(text).then(() => setCopied(text)).catch(() => setCopied(null));
  }, []);
  return [copied, copy];
}
