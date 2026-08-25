// CM constraint shaping this file: decorations affecting vertical layout MUST come
// from a StateField via `EditorView.decorations.from(...)`, never a view plugin.

import { ensureSyntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range, StateField } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view';
import type { SyntaxNode } from '@lezer/common';

export type CellAlign = 'left' | 'center' | 'right' | null;

export interface TableData {
  header: string[];
  align: CellAlign[];
  rows: string[][];
  fromLine: number;
}

function cellAlign(spec: string): CellAlign {
  const left = spec.startsWith(':');
  const right = spec.endsWith(':');
  if (left && right) return 'center';
  if (right) return 'right';
  if (left) return 'left';
  return null;
}

function cellTexts(row: SyntaxNode, state: EditorState): string[] {
  const cells: string[] = [];
  for (let cell = row.firstChild; cell; cell = cell.nextSibling) {
    if (cell.name === 'TableCell') cells.push(state.doc.sliceString(cell.from, cell.to).trim());
  }
  return cells;
}

function findTableNode(state: EditorState, from: number, to: number): SyntaxNode | null {
  const tree = ensureSyntaxTree(state, to, 50);
  if (!tree) return null;
  let found: SyntaxNode | null = null;
  tree.iterate({
    from,
    to,
    enter: (node) => {
      if (found) return false;
      if (node.name === 'Table' && node.from === from && node.to === to) {
        found = node.node;
        return false;
      }
      return undefined;
    },
  });
  return found;
}

export function parseTableData(state: EditorState, from: number, to: number): TableData | null {
  const table = findTableNode(state, from, to);
  if (!table) return null;

  let header: string[] | null = null;
  let align: CellAlign[] | null = null;
  const rows: string[][] = [];

  for (let child = table.firstChild; child; child = child.nextSibling) {
    if (child.name === 'TableHeader') {
      header = cellTexts(child, state);
    } else if (child.name === 'TableDelimiter') {
      align = state.doc
        .sliceString(child.from, child.to)
        .split('|')
        .map((s) => s.trim())
        .filter((s) => s.length > 0)
        .map(cellAlign);
    } else if (child.name === 'TableRow') {
      rows.push(cellTexts(child, state));
    }
  }
  if (!header || !align) return null;

  return { header, align, rows, fromLine: state.doc.lineAt(from).number };
}

class TableWidget extends WidgetType {
  constructor(readonly data: TableData) {
    super();
  }

  eq(other: TableWidget) {
    return JSON.stringify(this.data) === JSON.stringify(other.data);
  }

  get estimatedHeight() {
    return (1 + this.data.rows.length) * 26 + 8;
  }

  // Clicks must reach the editor so a row click can hand off to raw editing.
  ignoreEvent() {
    return false;
  }

  toDOM(view: EditorView) {
    const { header, align, rows, fromLine } = this.data;
    const lastLine = view.state.doc.lines;

    const gotoLine = (line: number) => {
      const clamped = Math.max(1, Math.min(line, lastLine));
      const target = view.state.doc.line(clamped).from;
      view.dispatch({ selection: { anchor: target } });
      view.focus();
    };

    const table = document.createElement('table');
    table.className = 'cm-md-table';

    const thead = document.createElement('thead');
    const headRow = document.createElement('tr');
    headRow.addEventListener('mousedown', (event) => event.preventDefault());
    headRow.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      gotoLine(fromLine);
    });
    header.forEach((text, i) => {
      const th = document.createElement('th');
      th.textContent = text;
      const a = align[i];
      if (a) th.style.textAlign = a;
      headRow.appendChild(th);
    });
    thead.appendChild(headRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    rows.forEach((cells, rowIndex) => {
      const tr = document.createElement('tr');
      tr.addEventListener('mousedown', (event) => event.preventDefault());
      tr.addEventListener('click', (event) => {
        event.preventDefault();
        event.stopPropagation();
        gotoLine(fromLine + 2 + rowIndex);
      });
      cells.forEach((text, i) => {
        const td = document.createElement('td');
        td.textContent = text;
        const a = align[i];
        if (a) td.style.textAlign = a;
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);

    return table;
  }
}

function tableDecorations(state: EditorState): DecorationSet {
  const tree = ensureSyntaxTree(state, state.doc.length, 50);
  if (!tree) return Decoration.none;

  const ranges: Range<Decoration>[] = [];
  tree.iterate({
    enter: (node) => {
      if (node.name !== 'Table') return undefined;
      const { from, to } = node;
      const revealed = state.selection.ranges.some(
        (range) => range.from <= to && range.to >= from,
      );
      if (!revealed) {
        const data = parseTableData(state, from, to);
        if (data) {
          ranges.push(Decoration.replace({ block: true, widget: new TableWidget(data) }).range(from, to));
        }
      }
      return false;
    },
  });
  return Decoration.set(ranges);
}

const tableField = StateField.define<DecorationSet>({
  create: (state) => tableDecorations(state),
  update(value, tr) {
    if (tr.docChanged || tr.selection) {
      const tree = ensureSyntaxTree(tr.state, tr.state.doc.length, 50);
      if (!tree) return value.map(tr.changes);
      return tableDecorations(tr.state);
    }
    return value.map(tr.changes);
  },
  provide: (field) => EditorView.decorations.from(field),
});

const tableTheme = EditorView.baseTheme({
  '.cm-md-table': {
    borderCollapse: 'collapse',
    margin: '4px 0',
    fontSize: '13px',
  },
  '.cm-md-table th, .cm-md-table td': {
    border: '1px solid var(--color-border, rgba(127,127,127,0.35))',
    padding: '3px 10px',
  },
  '.cm-md-table th': {
    backgroundColor: 'var(--color-bg-elevated, rgba(128,128,128,0.08))',
    fontWeight: '600',
  },
  '.cm-md-table tr': {
    cursor: 'pointer',
  },
});

export function markdownTables(): Extension {
  return [tableField, tableTheme];
}
