
import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef } from 'react';
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { languages } from '@codemirror/language-data';
import { syntaxHighlighting } from '@codemirror/language';
import { closeSearchPanel, search, searchKeymap, searchPanelOpen } from '@codemirror/search';
import { classHighlighter } from '@lezer/highlight';
import { EditorView, keymap, type KeyBinding, type ViewUpdate } from '@codemirror/view';
import { brokenLinks, revalidateBrokenLinks, type ExistsCheck } from './brokenLinks';
import { formattingKeymap } from './formatting';
import { frontmatterCard } from './frontmatterCard';
import { imageWidget } from './imageWidget';
import { liveMarkdownPreview } from './liveMarkdownPreview';
import { noteDir } from './linkResolver';
import { markdownTables } from './tableWidget';
import { computeMinimalEdit } from './minimalEdit';

// CM resolves "Mod-" to Meta only when it detects a Mac platform, so a Linux CI
// browser rebinds Cmd+F to Ctrl+F. attn is macOS only: rewrite "Mod-" to "Cmd-".
const macSearchKeymap: readonly KeyBinding[] = searchKeymap.map((binding) =>
  binding.key?.startsWith('Mod-') ? { ...binding, key: `Cmd-${binding.key.slice(4)}` } : binding,
);

export interface LiveSelection {
  text: string;
  top: number;
  left: number;
}

export interface LiveMarkdownEditorHandle {
  scrollToPos: (pos: number) => void;
  // Replace as a MINIMAL edit so scroll and selection stay anchored: the
  // controlled-value path swaps the whole document and snaps to the top.
  applyExternalContent: (next: string) => void;
  closeSearchPanel: () => boolean;
  focus: () => void;
}

interface LiveMarkdownEditorProps {
  value: string;
  onChange: (value: string) => void;
  onFollowLink?: (href: string) => void;
  onSelectionChange?: (selection: LiveSelection | null) => void;
  existsFile?: (path: string) => Promise<ExistsCheck>;
  resolveImageSrc?: (src: string) => Promise<string | null>;
  revalidateSignal?: number;
  notePath?: string;
  ariaLabel?: string;
  autoFocus?: boolean;
  onSearchOpenChange?: (open: boolean) => void;
}

// CM's base theme styles cursor/selection from `&light`/`&dark` rules that only
// activate with the darkTheme facet, so this theme must own all four colors.
const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'transparent',
    color: 'var(--color-text-primary, inherit)',
    fontSize: '14px',
  },
  '.cm-content': {
    fontFamily:
      "ui-serif, Georgia, 'Times New Roman', var(--font-sans, system-ui), serif",
    lineHeight: '1.65',
    padding: '4px 0 80px',
  },
  '.cm-scroller': { overflow: 'auto' },
  '&.cm-focused': { outline: 'none' },
  '.cm-line': { padding: '0 2px' },
  // drawSelection draws its own `.cm-cursor`, so the caret color is that
  // element's left border, and the selector must outrank CM's base rule.
  '.cm-cursorLayer .cm-cursor, .cm-dropCursor': {
    borderLeftColor: 'var(--color-text-primary, #e8e8e8)',
    borderLeftWidth: '2px',
  },
  // CM's base focused-selection rule is more specific than a theme can match, so
  // !important is the clean way to assert the app accent.
  '.cm-selectionLayer .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 30%, transparent) !important',
  },
  // CM's base theme paints its ⌘F buttons with a gradient; clear it explicitly.
  '.cm-panels': {
    color: 'var(--color-text-primary, inherit)',
    backgroundColor: 'var(--color-bg-elevated, rgba(128, 128, 128, 0.08))',
  },
  '.cm-panels.cm-panels-top': {
    borderBottom: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
  },
  '.cm-panel.cm-search': {
    padding: '4px 6px',
    fontFamily: 'system-ui, -apple-system, sans-serif',
    fontSize: '12px',
  },
  '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-search label': {
    fontFamily: 'inherit',
    fontSize: 'inherit',
    color: 'var(--color-text-primary, inherit)',
  },
  '.cm-panel.cm-search input': {
    backgroundColor: 'var(--color-bg-input, transparent)',
    border: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
    borderRadius: '3px',
    padding: '2px 4px',
  },
  '.cm-panel.cm-search button': {
    backgroundImage: 'none',
    backgroundColor: 'var(--color-bg-button, transparent)',
    border: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
    borderRadius: '3px',
  },
  '.cm-searchMatch': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 25%, transparent)',
  },
  '.cm-searchMatch-selected': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 50%, transparent)',
  },
});

export const LiveMarkdownEditor = forwardRef<LiveMarkdownEditorHandle, LiveMarkdownEditorProps>(function LiveMarkdownEditor({
  value,
  onChange,
  onFollowLink,
  onSelectionChange,
  existsFile,
  resolveImageSrc,
  revalidateSignal,
  notePath,
  ariaLabel,
  autoFocus,
  onSearchOpenChange,
}, ref) {
  const cmRef = useRef<ReactCodeMirrorRef>(null);
  const searchOpenRef = useRef(false);

  useImperativeHandle(ref, () => ({
    scrollToPos: (pos: number) => {
      const view = cmRef.current?.view;
      if (!view) return;
      // Clamp: a click can race a keystroke that shortened the doc, which throws.
      const target = Math.max(0, Math.min(pos, view.state.doc.length));
      view.dispatch({
        selection: { anchor: target },
        effects: EditorView.scrollIntoView(target, { y: 'start' }),
      });
      view.focus();
    },
    applyExternalContent: (next: string) => {
      const view = cmRef.current?.view;
      if (!view) return;
      const edit = computeMinimalEdit(view.state.doc.toString(), next);
      if (!edit) return;
      view.dispatch({
        changes: { from: edit.from, to: edit.to, insert: edit.insert },
        scrollIntoView: false,
      });
    },
    closeSearchPanel: () => {
      const view = cmRef.current?.view;
      if (!view || !searchPanelOpen(view.state)) return false;
      closeSearchPanel(view);
      return true;
    },
    focus: () => {
      cmRef.current?.view?.focus();
    },
  }), []);

  const extensions = useMemo(
    () => [
      markdown({ base: markdownLanguage, codeLanguages: languages }),
      syntaxHighlighting(classHighlighter),
      EditorView.lineWrapping,
      frontmatterCard(),
      markdownTables(),
      imageWidget({ resolveSrc: resolveImageSrc }),
      liveMarkdownPreview({ onFollowLink }),
      brokenLinks({ existsFile, baseDir: noteDir(notePath ?? '') }),
      search({ top: true }),
      keymap.of(macSearchKeymap),
      formattingKeymap(),
      editorTheme,
    ],
    [onFollowLink, existsFile, resolveImageSrc, notePath],
  );

  const didMountRef = useRef(false);
  useEffect(() => {
    if (!didMountRef.current) {
      didMountRef.current = true;
      return;
    }
    cmRef.current?.view?.dispatch({ effects: revalidateBrokenLinks.of(null) });
  }, [revalidateSignal]);

  const handleUpdate = useMemo(
    () =>
      (update: ViewUpdate) => {
        if (onSearchOpenChange) {
          const isOpen = searchPanelOpen(update.state);
          if (isOpen !== searchOpenRef.current) {
            searchOpenRef.current = isOpen;
            onSearchOpenChange(isOpen);
          }
        }
        if (!onSelectionChange) return;
        if (!update.selectionSet && !update.docChanged && !update.focusChanged) return;
        const range = update.state.selection.main;
        if (range.empty) {
          onSelectionChange(null);
          return;
        }
        const text = update.state.sliceDoc(range.from, range.to).trim();
        if (!text) {
          onSelectionChange(null);
          return;
        }
        const coords = update.view.coordsAtPos(range.to);
        if (!coords) {
          onSelectionChange(null);
          return;
        }
        onSelectionChange({ text, top: coords.bottom, left: (coords.left + coords.right) / 2 });
      },
    [onSelectionChange, onSearchOpenChange],
  );

  return (
    <CodeMirror
      ref={cmRef}
      value={value}
      onChange={onChange}
      onUpdate={handleUpdate}
      extensions={extensions}
      autoFocus={autoFocus}
      height="100%"
      aria-label={ariaLabel}
      // Skip @uiw/react-codemirror's built-in theme, whose "light" default paints a
      // white background; editorTheme keeps the surface transparent.
      theme="none"
      basicSetup={{
        lineNumbers: false,
        foldGutter: false,
        highlightActiveLine: false,
        highlightActiveLineGutter: false,
        highlightSelectionMatches: false,
        bracketMatching: false,
        closeBrackets: false,
        autocompletion: false,
        searchKeymap: false,
        lintKeymap: false,
        // The default highlight style fights the live-preview decorations, which
        // own how rendered markdown looks.
        syntaxHighlighting: false,
      }}
    />
  );
});
