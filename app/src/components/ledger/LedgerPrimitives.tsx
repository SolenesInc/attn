import { useCallback, useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, ReactNode } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';

export type RowGlyph =
  | 'live' | 'working' | 'waiting' | 'closed' | 'refreshing'
  | 'pinned' | 'scheduled' | 'dirty' | 'clean' | 'removed' | 'error';

export interface RowChoice {
  id: string;
  label: string;
}

export interface RowVerb {
  id: string;
  label: string;
  danger?: boolean;
  choices?: { title: string; options: RowChoice[] };
}

export interface LedgerMenu {
  key: string;
  choosing?: string;
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
  meta: ReactNode[];
  stamp?: { text: string; hint?: string };
  note?: RowNote;
  verbs: RowVerb[];
  dim?: boolean;
  yank?: string;
  attrs?: Record<string, string>;
}

export type ListItem =
  | { kind: 'group'; key: string; title: string; meta?: ReactNode }
  | { kind: 'row'; row: RowModel };

interface LedgerListProps {
  items: ListItem[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  onVerb: (key: string, verbId: string, choiceId?: string) => void;
  onEnter?: (key: string) => void;
  menu: LedgerMenu | null;
  onMenu: (menu: LedgerMenu | null) => void;
  onYank?: (text: string) => void;
  empty?: ReactNode;
}

export function LedgerList({
  items, selectedKey, onSelect, onVerb, onEnter, menu, onMenu, onYank, empty,
}: LedgerListProps) {
  const listRef = useRef<HTMLDivElement>(null);
  const rows = items.filter((item): item is Extract<ListItem, { kind: 'row' }> => item.kind === 'row');

  const focusRow = useCallback((key: string) => {
    const node = listRef.current?.querySelector<HTMLElement>(`[data-row-key="${CSS.escape(key)}"]`);
    node?.focus({ preventScroll: true });
    node?.scrollIntoView?.({ block: 'nearest' });
  }, []);

  // An open menu is the top Escape layer; it closes before the surface does.
  useEscapeStack(() => onMenu(null), menu !== null);

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

  const pick = useCallback((row: RowModel, verb: RowVerb) => {
    if (verb.choices) onMenu({ key: row.key, choosing: verb.id });
    else onVerb(row.key, verb.id);
  }, [onMenu, onVerb]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>, row: RowModel) => {
    const busy = row.note?.kind === 'busy';
    const menuOpen = menu?.key === row.key;
    const choosing = menuOpen ? row.verbs.find((verb) => verb.id === menu.choosing) : undefined;
    if (event.key === 'ArrowDown' || event.key === 'j') { event.preventDefault(); move(1); return; }
    if (event.key === 'ArrowUp' || event.key === 'k') { event.preventDefault(); move(-1); return; }
    if (event.key === 'Home') { event.preventDefault(); move(-rows.length); return; }
    if (event.key === 'End') { event.preventDefault(); move(rows.length); return; }
    if (event.key === 'Enter') {
      if (event.target !== event.currentTarget) return;
      event.preventDefault();
      if (onEnter) onEnter(row.key);
      else if (!busy && row.verbs[0]) pick(row, row.verbs[0]);
      return;
    }
    if (event.key === '.' || (event.key === 'ArrowRight' && row.verbs.length > 1)) {
      if (row.verbs.length > 1) { event.preventDefault(); onMenu(menuOpen ? null : { key: row.key }); }
      return;
    }
    if (event.key === 'ArrowLeft' && menuOpen) { event.preventDefault(); onMenu(null); return; }
    if (/^[1-9]$/.test(event.key)) {
      const index = Number(event.key) - 1;
      if (choosing) {
        const choice = choosing.choices?.options[index];
        if (choice && !busy) { event.preventDefault(); onVerb(row.key, choosing.id, choice.id); }
        return;
      }
      const verb = row.verbs[index];
      if (verb && !busy) { event.preventDefault(); pick(row, verb); }
      return;
    }
    if (event.key === 'y' && row.yank && onYank) { event.preventDefault(); onYank(row.yank); }
  }, [move, rows.length, onEnter, onVerb, onMenu, menu, onYank, pick]);

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
            menu={menu?.key === item.row.key ? menu : null}
            onSelect={() => onSelect(item.row.key)}
            onKeyDown={(event) => onKeyDown(event, item.row)}
            onPick={(verb) => pick(item.row, verb)}
            onChoose={(verbId, choiceId) => onVerb(item.row.key, verbId, choiceId)}
            onToggleMenu={() => onMenu(menu?.key === item.row.key ? null : { key: item.row.key })}
          />
        ))}
    </div>
  );
}

interface LedgerRowProps {
  row: RowModel;
  selected: boolean;
  menu: LedgerMenu | null;
  onSelect: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
  onPick: (verb: RowVerb) => void;
  onChoose: (verbId: string, choiceId: string) => void;
  onToggleMenu: () => void;
}

function LedgerRow({ row, selected, menu, onSelect, onKeyDown, onPick, onChoose, onToggleMenu }: LedgerRowProps) {
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
          {row.meta.map((segment, index) => (segment === null || segment === undefined || segment === '')
            ? null
            : <span className="ledger-meta-seg" key={`${row.key}:${index}`}>{segment}</span>)}
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
            onClick={(event) => { event.stopPropagation(); onPick(primary); }}
          >
            {busy ? row.note?.text : primary.label}
          </button>
        )}
        <RowMenu row={row} menu={menu} busy={busy} onPick={onPick} onChoose={onChoose} onToggleMenu={onToggleMenu} />
      </div>
    </div>
  );
}

function RowMenu({ row, menu, busy, onPick, onChoose, onToggleMenu }: {
  row: RowModel;
  menu: LedgerMenu | null;
  busy: boolean;
  onPick: (verb: RowVerb) => void;
  onChoose: (verbId: string, choiceId: string) => void;
  onToggleMenu: () => void;
}) {
  const choosing = row.verbs.find((verb) => verb.id === menu?.choosing);
  const hasMore = row.verbs.length > 1;
  if (!hasMore && !choosing) return null;
  return (
    <span className="ledger-menu-anchor">
      {hasMore && (
        <button
          type="button"
          className="ledger-more"
          aria-label={`More for ${row.title}`}
          aria-expanded={menu !== null}
          disabled={busy}
          onClick={(event) => { event.stopPropagation(); onToggleMenu(); }}
        >
          ···
        </button>
      )}
      {menu && (choosing
        ? <ChoiceMenu verb={choosing} onChoose={(choiceId) => onChoose(choosing.id, choiceId)} />
        : <VerbMenu verbs={row.verbs} onPick={onPick} />)}
    </span>
  );
}

function VerbMenu({ verbs, onPick }: { verbs: RowVerb[]; onPick: (verb: RowVerb) => void }) {
  return (
    <ul className="ledger-menu" role="menu">
      {verbs.map((verb, index) => (
        <li key={verb.id} role="none">
          <button
            type="button"
            role="menuitem"
            className={verb.danger ? 'is-danger' : undefined}
            onClick={(event) => { event.stopPropagation(); onPick(verb); }}
          >
            <kbd>{index + 1}</kbd>{verb.label}
          </button>
        </li>
      ))}
    </ul>
  );
}

function ChoiceMenu({ verb, onChoose }: { verb: RowVerb; onChoose: (choiceId: string) => void }) {
  const title = verb.choices?.title ?? verb.label;
  return (
    <ul className="ledger-menu" role="menu" aria-label={title}>
      <li role="presentation" className="ledger-menu-title">{title}</li>
      {verb.choices?.options.map((choice, index) => (
        <li key={choice.id} role="none">
          <button
            type="button"
            role="menuitem"
            onClick={(event) => { event.stopPropagation(); onChoose(choice.id); }}
          >
            <kbd>{index + 1}</kbd>{choice.label}
          </button>
        </li>
      ))}
    </ul>
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
