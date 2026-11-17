import { useEffect, useState } from 'react';
import type { ThemeRegistration } from 'shiki';
import './shiki.css';

let shikiModule: Promise<typeof import('shiki') | null> | null = null;
export function loadShiki() {
  shikiModule ??= import('shiki').catch((error) => {
    console.warn('[Markdown] Failed to load shiki:', error);
    return null;
  });
  return shikiModule;
}

export type Shiki = NonNullable<Awaited<ReturnType<typeof loadShiki>>>;

let schemeTheme: ThemeRegistration | null = null;
export function schemeCodeTheme(shiki: Shiki): ThemeRegistration {
  schemeTheme ??= shiki.createCssVariablesTheme({ name: 'attn-scheme' });
  return schemeTheme;
}

export interface Highlighted {
  /** The inputs the html was generated from — stale results never render. */
  code: string;
  language: string;
  html: string;
}

export function useShikiHighlight(
  code: string,
  language: string | undefined,
): Highlighted | null {
  const [highlighted, setHighlighted] = useState<Highlighted | null>(null);

  useEffect(() => {
    if (!language) {
      setHighlighted(null);
      return;
    }
    let cancelled = false;
    void loadShiki().then(async (shiki) => {
      if (!shiki || cancelled) return;
      try {
        const raw = await shiki.codeToHtml(code, {
          lang: language,
          theme: schemeCodeTheme(shiki),
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
  }, [code, language]);

  return highlighted && highlighted.code === code && highlighted.language === language
    ? highlighted
    : null;
}
