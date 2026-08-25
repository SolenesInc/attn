
import { ensureSyntaxTree, syntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
  WidgetType,
} from '@codemirror/view';
import type { Tree } from '@lezer/common';
import { parseFrontmatterFromDoc } from './frontmatter';

function frontmatterEnd(state: EditorState): number {
  const fm = parseFrontmatterFromDoc(state.doc);
  // Must agree with frontmatterCard's notion of frontmatter: an unclosed `---` is
  // NOT frontmatter to either extension.
  return fm ? fm.to : 0;
}

export interface LiveMarkdownOptions {
  onFollowLink?: (href: string) => void;
}

const HEADING_LEVEL: Record<string, number> = {
  ATXHeading1: 1,
  ATXHeading2: 2,
  ATXHeading3: 3,
  ATXHeading4: 4,
  ATXHeading5: 5,
  ATXHeading6: 6,
};

const STRONG = Decoration.mark({ class: 'cm-md-strong' });
const EMPHASIS = Decoration.mark({ class: 'cm-md-em' });
const INLINE_CODE = Decoration.mark({ class: 'cm-md-code' });
const STRIKE = Decoration.mark({ class: 'cm-md-strike' });
const HEADING_MARK = [1, 2, 3, 4, 5, 6].map((n) => Decoration.mark({ class: `cm-md-h${n}` }));
const HIDE = Decoration.replace({});
const CODEFENCE = Decoration.mark({ class: 'cm-md-codefence' });
const CODEINFO = Decoration.mark({ class: 'cm-md-codeinfo' });
// Scanning only the exact viewport let a heading cross the boundary during scroll
// anchoring, changing its line height and nudging the reader by several pixels.
const DECORATION_MARGIN = 5000;
const CODEBLOCK_LINE = Decoration.line({ class: 'cm-md-codeblock' });
const BLOCKQUOTE_LINE = Decoration.line({ class: 'cm-md-blockquote' });

function linkMark(href: string): Decoration {
  return Decoration.mark({
    class: 'cm-md-link',
    attributes: { 'data-href': href },
  });
}

class GlyphWidget extends WidgetType {
  constructor(readonly glyph: string, readonly cls: string) {
    super();
  }
  eq(other: GlyphWidget) {
    return other.glyph === this.glyph && other.cls === this.cls;
  }
  toDOM() {
    const span = document.createElement('span');
    span.className = this.cls;
    span.textContent = this.glyph;
    span.setAttribute('aria-hidden', 'true');
    return span;
  }
}

class CheckboxWidget extends WidgetType {
  readonly cls = 'cm-md-checkbox';
  constructor(readonly checked: boolean, readonly pos: number) {
    super();
  }
  eq(other: CheckboxWidget) {
    return other.checked === this.checked && other.pos === this.pos;
  }
  toDOM() {
    const span = document.createElement('span');
    span.className = `cm-md-checkbox${this.checked ? ' is-checked' : ''}`;
    span.textContent = this.checked ? '☑' : '☐';
    span.dataset.pos = String(this.pos);
    span.setAttribute('role', 'checkbox');
    span.setAttribute('aria-checked', this.checked ? 'true' : 'false');
    return span;
  }
  // Let the mousedown reach the editor-level handler so a click toggles the task.
  ignoreEvent() {
    return false;
  }
}

class HrWidget extends WidgetType {
  readonly cls = 'cm-md-hr';
  eq() {
    return true;
  }
  toDOM() {
    const span = document.createElement('span');
    span.className = this.cls;
    span.setAttribute('aria-hidden', 'true');
    return span;
  }
}

function activeLines(state: EditorState): Set<number> {
  const lines = new Set<number>();
  for (const range of state.selection.ranges) {
    const first = state.doc.lineAt(range.from).number;
    const last = state.doc.lineAt(range.to).number;
    for (let n = first; n <= last; n += 1) lines.add(n);
  }
  return lines;
}

// `focused` gates the active-line reveal: CM always keeps a selection at position 0,
// so an unfocused editor must render fully clean.
interface DecorationRange {
  from: number;
  to: number;
}

export function buildDecorations(
  state: EditorState,
  focused = true,
  parsedTree?: Tree,
  range?: DecorationRange,
): DecorationSet {
  const decos: Range<Decoration>[] = [];
  const { doc } = state;
  const active = focused ? activeLines(state) : new Set<number>();
  // The live ViewPlugin passes an already-ensured tree plus its viewport, so cursor
  // motion never reparses an arbitrarily large note synchronously.
  const tree = parsedTree ?? ensureSyntaxTree(state, doc.length, 100) ?? syntaxTree(state);
  const scanRange = range ?? { from: 0, to: doc.length };
  const fmEnd = frontmatterEnd(state);

  const onActiveLine = (pos: number) => active.has(doc.lineAt(pos).number);

  tree.iterate({
    from: scanRange.from,
    to: scanRange.to,
    enter: (node) => {
      if (node.from < fmEnd) return;

      const name = node.name;

      const level = HEADING_LEVEL[name];
      if (level) {
        decos.push(HEADING_MARK[level - 1].range(node.from, node.to));
        return;
      }

      if (name === 'FencedCode') {
        // node.to - 1 avoids grabbing the blank line after a block that ends on a
        // newline boundary.
        const firstLine = doc.lineAt(node.from).number;
        const lastLine = doc.lineAt(Math.max(node.from, node.to - 1)).number;
        for (let n = firstLine; n <= lastLine; n += 1) {
          decos.push(CODEBLOCK_LINE.range(doc.line(n).from));
        }
        return;
      }
      if (name === 'CodeInfo') {
        decos.push(CODEINFO.range(node.from, node.to));
        return;
      }

      if (name === 'Blockquote') {
        // Do NOT return here — nested content (including a nested Blockquote) must
        // still be walked so its own marks and styling apply.
        const firstLine = doc.lineAt(node.from).number;
        const lastLine = doc.lineAt(Math.max(node.from, node.to - 1)).number;
        for (let n = firstLine; n <= lastLine; n += 1) {
          decos.push(BLOCKQUOTE_LINE.range(doc.line(n).from));
        }
      }
      if (name === 'QuoteMark') {
        if (onActiveLine(node.from)) return;
        let to = node.to;
        if (doc.sliceString(to, to + 1) === ' ') to += 1;
        decos.push(HIDE.range(node.from, to));
        return;
      }

      if (name === 'HorizontalRule') {
        if (onActiveLine(node.from)) return;
        decos.push(Decoration.replace({ widget: new HrWidget() }).range(node.from, node.to));
        return;
      }

      if (name === 'ListMark') {
        const marker = doc.sliceString(node.from, node.to);
        if (marker !== '-' && marker !== '*' && marker !== '+') return;
        if (onActiveLine(node.from)) return;
        if (node.node.parent?.getChild('Task')) {
          let to = node.to;
          if (doc.sliceString(to, to + 1) === ' ') to += 1;
          decos.push(HIDE.range(node.from, to));
        } else {
          decos.push(
            Decoration.replace({ widget: new GlyphWidget('•', 'cm-md-bullet') }).range(node.from, node.to),
          );
        }
        return;
      }
      if (name === 'TaskMarker') {
        if (onActiveLine(node.from)) return;
        const checked = /\[[xX]\]/.test(doc.sliceString(node.from, node.to));
        decos.push(
          Decoration.replace({ widget: new CheckboxWidget(checked, node.from) }).range(node.from, node.to),
        );
        return;
      }

      if (name === 'StrongEmphasis') {
        decos.push(STRONG.range(node.from, node.to));
        return;
      }
      if (name === 'Emphasis') {
        decos.push(EMPHASIS.range(node.from, node.to));
        return;
      }
      if (name === 'InlineCode') {
        decos.push(INLINE_CODE.range(node.from, node.to));
        return;
      }
      if (name === 'Strikethrough') {
        decos.push(STRIKE.range(node.from, node.to));
        return;
      }
      if (name === 'Link') {
        const url = node.node.getChild('URL');
        const href = url ? doc.sliceString(url.from, url.to) : '';
        if (href) decos.push(linkMark(href).range(node.from, node.to));
        return;
      }

      if (name === 'HeaderMark') {
        // Only ATX leading `#`s (a Setext underline is also a HeaderMark — leave it).
        if (doc.sliceString(node.from, node.from + 1) !== '#') return;
        if (onActiveLine(node.from)) return;
        let to = node.to;
        if (doc.sliceString(to, to + 1) === ' ') to += 1;
        decos.push(HIDE.range(node.from, to));
        return;
      }
      if (name === 'EmphasisMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'StrikethroughMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'CodeMark') {
        if (node.node.parent?.name === 'InlineCode') {
          if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        } else {
          decos.push(CODEFENCE.range(node.from, node.to));
        }
        return;
      }
      if (name === 'LinkMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'URL') {
        if (node.node.parent?.name !== 'Link') return;
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
      }
    },
  });

  // CM requires decorations sorted by position (and side); let Decoration.set sort.
  return Decoration.set(decos, true);
}

const baseTheme = EditorView.baseTheme({
  '.cm-md-h1': { fontSize: '1.7em', fontWeight: '650', lineHeight: '1.3' },
  '.cm-md-h2': { fontSize: '1.4em', fontWeight: '630', lineHeight: '1.3' },
  '.cm-md-h3': { fontSize: '1.2em', fontWeight: '600' },
  '.cm-md-h4': { fontSize: '1.08em', fontWeight: '600' },
  '.cm-md-h5': { fontSize: '1em', fontWeight: '600' },
  '.cm-md-h6': { fontSize: '1em', fontWeight: '600', opacity: '0.85' },
  '.cm-md-strong': { fontWeight: '700' },
  '.cm-md-em': { fontStyle: 'italic' },
  '.cm-md-strike': { textDecoration: 'line-through', opacity: '0.75' },
  '.cm-md-code': {
    fontFamily:
      "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Monaco, Consolas, monospace",
    fontSize: '0.92em',
    padding: '0.5px 4px',
    borderRadius: '4px',
    background: 'var(--color-bg-elevated, rgba(127,127,127,0.16))',
  },
  '.cm-md-link': { color: 'var(--accent, #ff6b35)', cursor: 'pointer' },
  '.cm-md-bullet': { color: 'var(--accent, #ff6b35)' },
  '.cm-md-checkbox': { cursor: 'pointer', color: 'var(--color-text-secondary, #b8b8b8)' },
  '.cm-md-checkbox.is-checked': { color: 'var(--accent, #ff6b35)' },
  '.cm-md-codeblock': {
    fontFamily:
      "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Monaco, Consolas, monospace",
    fontSize: '0.9em',
    background: 'var(--color-bg-elevated, rgba(127,127,127,0.1))',
    borderLeft: '2px solid var(--color-border, rgba(127,127,127,0.35))',
  },
  '.cm-md-codefence': { opacity: '0.5' },
  '.cm-md-codeinfo': { opacity: '0.5', fontStyle: 'italic' },
  '.cm-md-blockquote': {
    borderLeft: '3px solid var(--accent, #ff6b35)',
    paddingLeft: '10px',
    color: 'var(--color-text-secondary, #b8b8b8)',
  },
  '.cm-md-hr': {
    display: 'inline-block',
    width: '100%',
    height: '1px',
    verticalAlign: 'middle',
    background: 'var(--color-border, rgba(127,127,127,0.35))',
  },
  '.cm-md-codeblock .tok-keyword': { color: 'var(--syntax-keyword, #c678dd)' },
  '.cm-md-codeblock .tok-string, .cm-md-codeblock .tok-string2': {
    color: 'var(--syntax-string, #98c379)',
  },
  '.cm-md-codeblock .tok-comment': {
    color: 'var(--syntax-comment, #7f848e)',
    fontStyle: 'italic',
  },
  '.cm-md-codeblock .tok-number': { color: 'var(--syntax-number, #d19a66)' },
  '.cm-md-codeblock .tok-bool, .cm-md-codeblock .tok-atom': {
    color: 'var(--syntax-atom, #56b6c2)',
  },
  '.cm-md-codeblock .tok-typeName, .cm-md-codeblock .tok-className, .cm-md-codeblock .tok-namespace': {
    color: 'var(--syntax-type, #e5c07b)',
  },
  '.cm-md-codeblock .tok-propertyName': { color: 'var(--syntax-property, #e06c75)' },
  '.cm-md-codeblock .tok-variableName2': { color: 'var(--syntax-function, #61afef)' },
  '.cm-md-codeblock .tok-operator, .cm-md-codeblock .tok-punctuation': {
    color: 'var(--color-text-secondary, #b8b8b8)',
  },
  '.cm-md-codeblock .tok-meta': { opacity: '0.7' },
});

export function liveMarkdownPreview(options: LiveMarkdownOptions = {}): Extension {
  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      tree: Tree;

      constructor(view: EditorView) {
        this.tree = syntaxTree(view.state);
        this.decorations = Decoration.none;
        this.rebuild(view);
      }

      rebuild(view: EditorView) {
        // CM parses only the first 3,000 characters on state creation, so ensuring through this
        // range stops a cursor transaction from replacing rendered markdown with raw text.
        const range = {
          from: Math.max(0, view.viewport.from - DECORATION_MARGIN),
          to: Math.min(view.state.doc.length, view.viewport.to + DECORATION_MARGIN),
        };
        const upto = range.to;
        this.tree = ensureSyntaxTree(view.state, upto, 20) ?? syntaxTree(view.state);
        this.decorations = buildDecorations(
          view.state,
          view.hasFocus,
          this.tree,
          range,
        );
      }

      update(update: ViewUpdate) {
        const currentTree = syntaxTree(update.state);
        // A parse-only transaction must also rebuild, so newly parsed visible syntax
        // becomes decorated without another user action.
        if (
          update.docChanged ||
          update.viewportChanged ||
          update.selectionSet ||
          update.focusChanged ||
          currentTree !== this.tree
        ) {
          this.rebuild(update.view);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );

  const followLinks = EditorView.domEventHandlers({
    mousedown: (event, view) => {
      if (!(event.metaKey || event.ctrlKey)) return false;
      const target = (event.target as HTMLElement | null)?.closest('.cm-md-link');
      const href = target?.getAttribute('data-href');
      if (!href) return false;
      event.preventDefault();
      options.onFollowLink?.(href);
      view.dispatch({ selection: { anchor: view.state.selection.main.head } });
      return true;
    },
  });

  // The state char sits at pos+1. preventDefault keeps the click from moving the cursor
  // onto the line, which would reveal the raw marker and unmount the checkbox mid-click.
  const toggleCheckbox = EditorView.domEventHandlers({
    mousedown: (event, view) => {
      const target = (event.target as HTMLElement | null)?.closest('.cm-md-checkbox');
      const pos = target?.getAttribute('data-pos');
      if (pos == null) return false;
      const stateFrom = Number(pos) + 1;
      if (Number.isNaN(stateFrom) || stateFrom >= view.state.doc.length) return false;
      const current = view.state.doc.sliceString(stateFrom, stateFrom + 1);
      const next = current.toLowerCase() === 'x' ? ' ' : 'x';
      event.preventDefault();
      view.dispatch({ changes: { from: stateFrom, to: stateFrom + 1, insert: next } });
      return true;
    },
  });

  return [plugin, baseTheme, followLinks, toggleCheckbox];
}
