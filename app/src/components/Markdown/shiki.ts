import { useEffect, useState } from 'react';

let shikiModule: Promise<typeof import('shiki') | null> | null = null;
function loadShiki() {
  shikiModule ??= import('shiki').catch((error) => {
    console.warn('[Markdown] Failed to load shiki:', error);
    return null;
  });
  return shikiModule;
}

export interface Highlighted {
  /** The inputs the html was generated from — stale results never render. */
  code: string;
  language: string;
  html: string;
}

// `enabled` is how a streaming surface opts out: highlighting text that is still
// arriving runs shiki once per delta and throws away every result but the last.
export function useShikiHighlight(
  code: string,
  language: string | undefined,
  enabled = true,
): Highlighted | null {
  const [highlighted, setHighlighted] = useState<Highlighted | null>(null);

  useEffect(() => {
    if (!language || !enabled) {
      setHighlighted(null);
      return;
    }
    let cancelled = false;
    void loadShiki().then(async (shiki) => {
      if (!shiki || cancelled) return;
      try {
        const raw = await shiki.codeToHtml(code, {
          lang: language,
          themes: { light: 'github-light-default', dark: 'github-dark-default' },
          defaultColor: false,
          structure: 'inline',
        });
        // Shiki renders line breaks as <br> elements, leaving no '\n' text;
        // MarkdownReader's anchoring needs text-node parity, so restore them.
        const html = raw.replace(/<br\s*\/?>/g, '\n');
        if (!cancelled) setHighlighted({ code, language, html });
      } catch {
        if (!cancelled) setHighlighted(null);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [code, language, enabled]);

  return highlighted && highlighted.code === code && highlighted.language === language
    ? highlighted
    : null;
}
