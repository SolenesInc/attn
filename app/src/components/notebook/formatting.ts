// Emphasis and StrongEmphasis are distinct Lezer node types, so toggling emphasis
// inside a bold run wraps rather than matching the enclosing run.

import { EditorSelection, Prec, type EditorState, type Extension, type TransactionSpec } from '@codemirror/state';
import { EditorView, keymap, type KeyBinding } from '@codemirror/view';
import { ensureSyntaxTree, syntaxTree } from '@codemirror/language';
import type { SyntaxNode } from '@lezer/common';
import { acceleratorBindings } from './acceleratorKeymap';

export type InlineMarkType = 'strong' | 'emphasis' | 'code';

interface MarkConfig {
  nodeName: 'StrongEmphasis' | 'Emphasis' | 'InlineCode';
  markNodeName: 'EmphasisMark' | 'CodeMark';
  marker: string;
}

const CONFIGS: Record<InlineMarkType, MarkConfig> = {
  strong: { nodeName: 'StrongEmphasis', markNodeName: 'EmphasisMark', marker: '**' },
  emphasis: { nodeName: 'Emphasis', markNodeName: 'EmphasisMark', marker: '*' },
  code: { nodeName: 'InlineCode', markNodeName: 'CodeMark', marker: '`' },
};

function findEnclosing(node: SyntaxNode, nodeName: string, from: number, to: number): SyntaxNode | null {
  let cur: SyntaxNode | null = node;
  while (cur) {
    if (cur.name === nodeName && cur.from <= from && cur.to >= to) return cur;
    cur = cur.parent;
  }
  return null;
}

function afterDeletions(
  pos: number,
  openFrom: number,
  openTo: number,
  closeFrom: number,
  closeTo: number,
): number {
  let result = pos;
  if (pos >= openTo) result -= openTo - openFrom;
  if (pos >= closeTo) result -= closeTo - closeFrom;
  return result;
}

export function toggleInlineFormat(state: EditorState, type: InlineMarkType): TransactionSpec | null {
  const config = CONFIGS[type];
  if (state.selection.ranges.length === 0) return null;

  return state.changeByRange((range) => {
    const tree = ensureSyntaxTree(state, range.to, 50) ?? syntaxTree(state);
    const resolved = tree.resolveInner(range.from, 1);
    const enclosing = findEnclosing(resolved, config.nodeName, range.from, range.to);
    const marks = enclosing?.getChildren(config.markNodeName) ?? [];

    if (enclosing && marks.length >= 2) {
      // Inline code can be fenced with several backticks, so use the marks' actual ranges.
      const open = marks[0];
      const close = marks[marks.length - 1];
      const changes = [
        { from: open.from, to: open.to, insert: '' },
        { from: close.from, to: close.to, insert: '' },
      ];
      const map = (pos: number) => afterDeletions(pos, open.from, open.to, close.from, close.to);
      return { changes, range: EditorSelection.range(map(range.from), map(range.to)) };
    }

    if (!range.empty) {
      const changes = [
        { from: range.from, insert: config.marker },
        { from: range.to, insert: config.marker },
      ];
      return {
        changes,
        range: EditorSelection.range(
          range.from + config.marker.length,
          range.to + config.marker.length,
        ),
      };
    }

    const word = state.wordAt(range.head);
    if (word) {
      const changes = [
        { from: word.from, insert: config.marker },
        { from: word.to, insert: config.marker },
      ];
      return { changes, range: EditorSelection.cursor(range.head + config.marker.length) };
    }

    const pair = config.marker + config.marker;
    return {
      changes: { from: range.head, insert: pair },
      range: EditorSelection.cursor(range.head + config.marker.length),
    };
  });
}

function toggleCommand(type: InlineMarkType) {
  return (view: EditorView): boolean => {
    const spec = toggleInlineFormat(view.state, type);
    if (!spec) return false;
    view.dispatch(spec);
    return true;
  };
}

export function formattingKeymap(): Extension {
  const bindings: KeyBinding[] = acceleratorBindings([
    { key: 'Mod-b', run: toggleCommand('strong') },
    { key: 'Mod-i', run: toggleCommand('emphasis') },
    { key: 'Mod-e', run: toggleCommand('code') },
  ]);
  // basicSetup's defaultKeymap binds Mod-i to selectParentSyntax with preventDefault and runs first at equal precedence.
  return Prec.high(keymap.of(bindings));
}
