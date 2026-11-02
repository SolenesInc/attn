import { DictationTextFormat } from '../types/generated';
import type { PasteTarget } from './target';

export interface TerminalBinding {
  runtimeId: string;
  sessionId: string;
}

export interface TerminalInput {
  binding(): TerminalBinding | null;
  paste(text: string, binding: TerminalBinding): boolean;
}

interface EnterClaim {
  binding: TerminalBinding;
  onEnter(): void;
}

const terminals = new WeakMap<Element, TerminalInput>();
const enterClaims = new WeakMap<Element, EnterClaim[]>();

const sameBinding = (a: TerminalBinding | null | undefined, b: TerminalBinding) =>
  a?.runtimeId === b.runtimeId && a.sessionId === b.sessionId;

export function registerTerminalInput(element: Element, input: TerminalInput) {
  terminals.set(element, input);
  return () => { if (terminals.get(element) === input) terminals.delete(element); };
}

export function consumeDictationEnter(element: Element, event: KeyboardEvent) {
  if (event.type !== 'keydown' || event.key !== 'Enter' || event.repeat || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) return false;
  const claims = enterClaims.get(element);
  if (!claims?.length) return false;
  const current = terminals.get(element)?.binding();
  const live = claims.filter(claim => sameBinding(current, claim.binding));
  enterClaims.set(element, live);
  const claim = live[0];
  if (!claim) return false;
  claim.onEnter();
  return true;
}

export function terminalTarget(focused: Element | null): PasteTarget | null {
  const element = focused?.closest('.ghostty-terminal');
  const input = element ? terminals.get(element) : undefined;
  const binding = input?.binding();
  if (!element || !input || !binding) return null;
  let claim: EnterClaim | null = null;
  const unclaim = () => {
    const claims = enterClaims.get(element);
    if (claims && claim) enterClaims.set(element, claims.filter(other => other !== claim));
    claim = null;
  };
  return {
    kind: 'paste',
    format: DictationTextFormat.Plain,
    paste: text => element.isConnected && terminals.get(element) === input && input.paste(text, binding) ? 'inserted' : 'targetGone',
    claimEnter: onEnter => {
      unclaim();
      claim = { binding, onEnter };
      enterClaims.set(element, [...(enterClaims.get(element) ?? []), claim]);
    },
    release: unclaim,
  };
}
