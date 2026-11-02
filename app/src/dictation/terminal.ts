import { DictationTextFormat } from '../types/generated';
import type { PasteTarget } from './target';

export interface TerminalInput {
  runtime(): string | null;
  paste(text: string, runtime: string): boolean;
}

const terminals = new WeakMap<Element, TerminalInput>();
const enterClaims = new WeakMap<Element, (() => void)[]>();

export function registerTerminalInput(element: Element, input: TerminalInput) {
  terminals.set(element, input);
  return () => { if (terminals.get(element) === input) terminals.delete(element); };
}

export function consumeDictationEnter(element: Element, event: KeyboardEvent) {
  if (event.type !== 'keydown' || event.key !== 'Enter' || event.repeat || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) return false;
  const onEnter = enterClaims.get(element)?.[0];
  if (!onEnter) return false;
  onEnter();
  return true;
}

export function terminalTarget(focused: Element | null): PasteTarget | null {
  const element = focused?.closest('.ghostty-terminal');
  const input = element ? terminals.get(element) : undefined;
  const runtime = input?.runtime();
  if (!element || !input || !runtime) return null;
  let claim: (() => void) | null = null;
  const unclaim = () => {
    const claims = enterClaims.get(element);
    if (claims && claim) claims.splice(claims.indexOf(claim), 1);
    claim = null;
  };
  return {
    kind: 'paste',
    format: DictationTextFormat.Plain,
    paste: text => element.isConnected && terminals.get(element) === input && input.paste(text, runtime) ? 'inserted' : 'targetGone',
    claimEnter: onEnter => {
      unclaim();
      claim = onEnter;
      const claims = enterClaims.get(element);
      if (claims) claims.push(onEnter);
      else enterClaims.set(element, [onEnter]);
    },
    release: unclaim,
  };
}
