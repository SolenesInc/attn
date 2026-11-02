import { DictationTextFormat } from '../types/generated';
import type { Insertion } from './field';
import type { EditTarget } from './target';

export type BrowserDictationAction = 'capture' | 'replace' | 'release';

export type BrowserDictationHost = (label: string, action: BrowserDictationAction, id?: string, text?: string) => Promise<string>;

const INSERTIONS = new Set<Insertion>(['inserted', 'targetGone', 'rangeEdited', 'refused']);

export async function captureBrowserTarget(host: BrowserDictationHost, label: string): Promise<EditTarget | { reason: string }> {
  let captured: { id?: string; reason?: string } | null;
  try {
    captured = JSON.parse(await host(label, 'capture'));
  } catch (error) {
    return { reason: `The browser page did not answer: ${error instanceof Error ? error.message : String(error)}` };
  }
  const id = captured?.id;
  if (!id) return { reason: captured?.reason ?? 'The browser page has no text field focused.' };
  return {
    kind: 'edit',
    format: DictationTextFormat.Plain,
    replace: text => host(label, 'replace', id, text).then(raw => {
      const outcome = JSON.parse(raw) as Insertion;
      return INSERTIONS.has(outcome) ? outcome : 'targetGone';
    }, () => 'targetGone' as const),
    release: () => { void host(label, 'release', id).catch(() => undefined); },
  };
}
