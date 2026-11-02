import { DictationTextFormat } from '../types/generated';
import type { Insertion } from './field';
import type { EditTarget, RichEditorPaste } from './target';

export type BrowserDictationAction = 'capture' | 'replace' | 'release';

export type BrowserDictationHost = (label: string, action: BrowserDictationAction, id?: string, text?: string) => Promise<string>;

const INSERTIONS = new Set<Insertion>(['inserted', 'targetGone', 'rangeEdited', 'refused']);

export async function captureBrowserTarget(host: BrowserDictationHost, label: string): Promise<EditTarget | RichEditorPaste | { reason: string }> {
  let captured: { id?: string; kind?: 'edit' | 'paste'; reason?: string } | null;
  try {
    captured = JSON.parse(await host(label, 'capture'));
  } catch (error) {
    return { reason: `The browser page did not answer: ${error instanceof Error ? error.message : String(error)}` };
  }
  const id = captured?.id;
  if (!id) return { reason: captured?.reason ?? 'The browser page has no text field focused.' };
  const replace = (text: string) => host(label, 'replace', id, text).then(raw => {
    const outcome = JSON.parse(raw) as Insertion;
    return INSERTIONS.has(outcome) ? outcome : 'targetGone';
  }, () => 'targetGone' as const);
  const release = () => { void host(label, 'release', id).catch(() => undefined); };
  if (captured?.kind === 'paste') {
    return {
      kind: 'paste',
      place: 'richEditor',
      format: DictationTextFormat.Plain,
      paste: text => replace(text).then(outcome => outcome === 'inserted' ? 'inserted' : 'targetGone'),
      release,
    };
  }
  return { kind: 'edit', format: DictationTextFormat.Plain, replace, release };
}
