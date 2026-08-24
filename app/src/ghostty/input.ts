import type { TerminalKeyEvent } from './keyEncoder';

const MOD_SHIFT = 1 << 0;
const MOD_CTRL = 1 << 1;
const MOD_ALT = 1 << 2;
const MOD_SUPER = 1 << 3;
const MOD_CAPS_LOCK = 1 << 4;
const MOD_NUM_LOCK = 1 << 5;
const MOD_SHIFT_RIGHT = 1 << 6;
const MOD_CTRL_RIGHT = 1 << 7;
const MOD_ALT_RIGHT = 1 << 8;
const MOD_SUPER_RIGHT = 1 << 9;

const KEY_BY_CODE: Readonly<Record<string, string>> = Object.freeze({
  Backquote: 'BACKQUOTE',
  Backslash: 'BACKSLASH',
  BracketLeft: 'BRACKET_LEFT',
  BracketRight: 'BRACKET_RIGHT',
  Comma: 'COMMA',
  Digit0: 'DIGIT_0',
  Digit1: 'DIGIT_1',
  Digit2: 'DIGIT_2',
  Digit3: 'DIGIT_3',
  Digit4: 'DIGIT_4',
  Digit5: 'DIGIT_5',
  Digit6: 'DIGIT_6',
  Digit7: 'DIGIT_7',
  Digit8: 'DIGIT_8',
  Digit9: 'DIGIT_9',
  Equal: 'EQUAL',
  IntlBackslash: 'INTL_BACKSLASH',
  IntlRo: 'INTL_RO',
  IntlYen: 'INTL_YEN',
  KeyA: 'A',
  KeyB: 'B',
  KeyC: 'C',
  KeyD: 'D',
  KeyE: 'E',
  KeyF: 'F',
  KeyG: 'G',
  KeyH: 'H',
  KeyI: 'I',
  KeyJ: 'J',
  KeyK: 'K',
  KeyL: 'L',
  KeyM: 'M',
  KeyN: 'N',
  KeyO: 'O',
  KeyP: 'P',
  KeyQ: 'Q',
  KeyR: 'R',
  KeyS: 'S',
  KeyT: 'T',
  KeyU: 'U',
  KeyV: 'V',
  KeyW: 'W',
  KeyX: 'X',
  KeyY: 'Y',
  KeyZ: 'Z',
  Minus: 'MINUS',
  Period: 'PERIOD',
  Quote: 'QUOTE',
  Semicolon: 'SEMICOLON',
  Slash: 'SLASH',

  AltLeft: 'ALT_LEFT',
  AltRight: 'ALT_RIGHT',
  Backspace: 'BACKSPACE',
  CapsLock: 'CAPS_LOCK',
  ContextMenu: 'CONTEXT_MENU',
  ControlLeft: 'CONTROL_LEFT',
  ControlRight: 'CONTROL_RIGHT',
  Enter: 'ENTER',
  MetaLeft: 'META_LEFT',
  MetaRight: 'META_RIGHT',
  OSLeft: 'META_LEFT',
  OSRight: 'META_RIGHT',
  ShiftLeft: 'SHIFT_LEFT',
  ShiftRight: 'SHIFT_RIGHT',
  Space: 'SPACE',
  Tab: 'TAB',
  Convert: 'CONVERT',
  KanaMode: 'KANA_MODE',
  NonConvert: 'NON_CONVERT',

  Delete: 'DELETE',
  End: 'END',
  Help: 'HELP',
  Home: 'HOME',
  Insert: 'INSERT',
  PageDown: 'PAGE_DOWN',
  PageUp: 'PAGE_UP',
  ArrowDown: 'ARROW_DOWN',
  ArrowLeft: 'ARROW_LEFT',
  ArrowRight: 'ARROW_RIGHT',
  ArrowUp: 'ARROW_UP',

  NumLock: 'NUM_LOCK',
  Numpad0: 'NUMPAD_0',
  Numpad1: 'NUMPAD_1',
  Numpad2: 'NUMPAD_2',
  Numpad3: 'NUMPAD_3',
  Numpad4: 'NUMPAD_4',
  Numpad5: 'NUMPAD_5',
  Numpad6: 'NUMPAD_6',
  Numpad7: 'NUMPAD_7',
  Numpad8: 'NUMPAD_8',
  Numpad9: 'NUMPAD_9',
  NumpadAdd: 'NUMPAD_ADD',
  NumpadBackspace: 'NUMPAD_BACKSPACE',
  NumpadClear: 'NUMPAD_CLEAR',
  NumpadClearEntry: 'NUMPAD_CLEAR_ENTRY',
  NumpadComma: 'NUMPAD_COMMA',
  NumpadDecimal: 'NUMPAD_DECIMAL',
  NumpadDivide: 'NUMPAD_DIVIDE',
  NumpadEnter: 'NUMPAD_ENTER',
  NumpadEqual: 'NUMPAD_EQUAL',
  NumpadMemoryAdd: 'NUMPAD_MEMORY_ADD',
  NumpadMemoryClear: 'NUMPAD_MEMORY_CLEAR',
  NumpadMemoryRecall: 'NUMPAD_MEMORY_RECALL',
  NumpadMemoryStore: 'NUMPAD_MEMORY_STORE',
  NumpadMemorySubtract: 'NUMPAD_MEMORY_SUBTRACT',
  NumpadMultiply: 'NUMPAD_MULTIPLY',
  NumpadParenLeft: 'NUMPAD_PAREN_LEFT',
  NumpadParenRight: 'NUMPAD_PAREN_RIGHT',
  NumpadSubtract: 'NUMPAD_SUBTRACT',
  NumpadSeparator: 'NUMPAD_SEPARATOR',

  Escape: 'ESCAPE',
  F1: 'F1',
  F2: 'F2',
  F3: 'F3',
  F4: 'F4',
  F5: 'F5',
  F6: 'F6',
  F7: 'F7',
  F8: 'F8',
  F9: 'F9',
  F10: 'F10',
  F11: 'F11',
  F12: 'F12',
  F13: 'F13',
  F14: 'F14',
  F15: 'F15',
  F16: 'F16',
  F17: 'F17',
  F18: 'F18',
  F19: 'F19',
  F20: 'F20',
  F21: 'F21',
  F22: 'F22',
  F23: 'F23',
  F24: 'F24',
  F25: 'F25',
  Fn: 'FN',
  FnLock: 'FN_LOCK',
  PrintScreen: 'PRINT_SCREEN',
  ScrollLock: 'SCROLL_LOCK',
  Pause: 'PAUSE',

  BrowserBack: 'BROWSER_BACK',
  BrowserFavorites: 'BROWSER_FAVORITES',
  BrowserForward: 'BROWSER_FORWARD',
  BrowserHome: 'BROWSER_HOME',
  BrowserRefresh: 'BROWSER_REFRESH',
  BrowserSearch: 'BROWSER_SEARCH',
  BrowserStop: 'BROWSER_STOP',
  Eject: 'EJECT',
  LaunchApp1: 'LAUNCH_APP_1',
  LaunchApp2: 'LAUNCH_APP_2',
  LaunchMail: 'LAUNCH_MAIL',
  MediaPlayPause: 'MEDIA_PLAY_PAUSE',
  MediaSelect: 'MEDIA_SELECT',
  MediaStop: 'MEDIA_STOP',
  MediaTrackNext: 'MEDIA_TRACK_NEXT',
  MediaTrackPrevious: 'MEDIA_TRACK_PREVIOUS',
  Power: 'POWER',
  Sleep: 'SLEEP',
  AudioVolumeDown: 'AUDIO_VOLUME_DOWN',
  AudioVolumeMute: 'AUDIO_VOLUME_MUTE',
  AudioVolumeUp: 'AUDIO_VOLUME_UP',
  VolumeDown: 'AUDIO_VOLUME_DOWN',
  VolumeMute: 'AUDIO_VOLUME_MUTE',
  VolumeUp: 'AUDIO_VOLUME_UP',
  WakeUp: 'WAKE_UP',
  Copy: 'COPY',
  Cut: 'CUT',
  Paste: 'PASTE',
});

export const TERMINAL_INPUT_KEY_NAMES = Object.freeze([...new Set(Object.values(KEY_BY_CODE))]);

export interface TerminalInputTarget {
  encodeKey(event: TerminalKeyEvent): string;
  formatPaste(text: string): string;
}

export interface TerminalInputOptions {
  element: HTMLElement;
  terminal: () => TerminalInputTarget | null;
  send: (data: string) => void;
  interceptKey: (event: KeyboardEvent) => boolean;
  onError: (operation: 'key' | 'paste', error: unknown) => void;
}

function modifierState(event: KeyboardEvent, name: string): boolean {
  try {
    return event.getModifierState(name);
  } catch {
    return false;
  }
}

function modifiers(event: KeyboardEvent): number {
  let result = 0;
  if (event.shiftKey) result |= MOD_SHIFT;
  if (event.ctrlKey) result |= MOD_CTRL;
  if (event.altKey) result |= MOD_ALT;
  if (event.metaKey) result |= MOD_SUPER;
  if (modifierState(event, 'CapsLock')) result |= MOD_CAPS_LOCK;
  if (modifierState(event, 'NumLock')) result |= MOD_NUM_LOCK;

  if (event.location === 2) {
    if (event.shiftKey && event.code === 'ShiftRight') result |= MOD_SHIFT_RIGHT;
    if (event.ctrlKey && event.code === 'ControlRight') result |= MOD_CTRL_RIGHT;
    if (event.altKey && event.code === 'AltRight') result |= MOD_ALT_RIGHT;
    if (event.metaKey && (event.code === 'MetaRight' || event.code === 'OSRight')) result |= MOD_SUPER_RIGHT;
  }
  return result;
}

function printableText(event: KeyboardEvent): string | undefined {
  if (event.altKey && !modifierState(event, 'AltGraph')) return undefined;
  const scalars = Array.from(event.key);
  if (scalars.length !== 1) return undefined;
  const codepoint = scalars[0].codePointAt(0) ?? 0;
  if (codepoint < 0x20 || codepoint === 0x7f) return undefined;
  if (codepoint >= 0xf700 && codepoint <= 0xf8ff) return undefined;
  return event.key;
}

function consumedModifiers(event: KeyboardEvent, text: string | undefined): number {
  if (!text) return 0;
  let result = 0;
  if (event.shiftKey) result |= MOD_SHIFT;
  if (modifierState(event, 'CapsLock')) result |= MOD_CAPS_LOCK;
  if (modifierState(event, 'NumLock')) result |= MOD_NUM_LOCK;
  if (modifierState(event, 'AltGraph')) result |= MOD_CTRL | MOD_ALT;
  return result;
}

const UNSHIFTED_BY_CODE: Readonly<Record<string, string>> = Object.freeze({
  Backquote: '`',
  Backslash: '\\',
  BracketLeft: '[',
  BracketRight: ']',
  Comma: ',',
  Equal: '=',
  Minus: '-',
  Period: '.',
  Quote: "'",
  Semicolon: ';',
  Slash: '/',
  Space: ' ',
});

function physicalCodepoint(code: string): number {
  const letter = /^Key([A-Z])$/.exec(code)?.[1];
  if (letter) return letter.toLowerCase().codePointAt(0) ?? 0;
  const digit = /^Digit([0-9])$/.exec(code)?.[1];
  if (digit) return digit.codePointAt(0) ?? 0;
  return UNSHIFTED_BY_CODE[code]?.codePointAt(0) ?? 0;
}

function unshiftedCodepoint(event: KeyboardEvent, text: string | undefined): number {
  if (!text) return physicalCodepoint(event.code);
  const lower = text.toLowerCase();
  if (lower !== text.toUpperCase() && Array.from(lower).length === 1) {
    return lower.codePointAt(0) ?? 0;
  }
  if (!event.shiftKey && !event.altKey && !event.metaKey && !modifierState(event, 'AltGraph')) {
    return text.codePointAt(0) ?? 0;
  }
  return 0;
}

function browserOwnsKey(event: KeyboardEvent): boolean {
  return ((event.metaKey || event.ctrlKey) && event.code === 'KeyV')
    || (event.metaKey && event.code === 'KeyC');
}

function consumeBrowserEvent(event: Event): void {
  event.preventDefault();
  event.stopPropagation();
}

function removeCompositionTextNodes(element: HTMLElement): void {
  for (let i = element.childNodes.length - 1; i >= 0; i -= 1) {
    const child = element.childNodes[i];
    if (child.nodeType === 3) element.removeChild(child);
  }
}

export function attachTerminalInput(options: TerminalInputOptions): () => void {
  const { element, terminal, send, interceptKey, onError } = options;
  const forwarded = new Map<string, TerminalInputTarget>();
  let composing = false;
  let disposed = false;

  if (!element.hasAttribute('tabindex')) element.setAttribute('tabindex', '0');

  const keydown = (event: KeyboardEvent) => {
    if (disposed || composing || event.isComposing || event.keyCode === 229) return;
    if (interceptKey(event)) {
      consumeBrowserEvent(event);
      return;
    }
    if (browserOwnsKey(event)) return;
    // Dead keys continue through WebKit's composition events.
    if (event.key === 'Dead') return;

    const text = printableText(event);
    const key = KEY_BY_CODE[event.code] ?? (text ? 'UNIDENTIFIED' : null);
    if (!key) return;

    const target = terminal();
    if (!target) {
      consumeBrowserEvent(event);
      return;
    }

    const id = event.code || event.key;
    forwarded.set(id, target);
    let data: string;
    try {
      data = target.encodeKey({
        action: event.repeat ? 'repeat' : 'press',
        key,
        mods: modifiers(event),
        consumedMods: consumedModifiers(event, text),
        composing: false,
        utf8: text,
        unshiftedCodepoint: unshiftedCodepoint(event, text),
      });
    } catch (error) {
      forwarded.delete(id);
      consumeBrowserEvent(event);
      onError('key', error);
      return;
    }
    if (data) {
      consumeBrowserEvent(event);
      send(data);
    }
  };

  const keyup = (event: KeyboardEvent) => {
    if (disposed) return;
    const id = event.code || event.key;
    const target = forwarded.get(id);
    if (!target) return;
    forwarded.delete(id);
    if (terminal() !== target) return;

    const text = printableText(event);
    const key = KEY_BY_CODE[event.code] ?? (text ? 'UNIDENTIFIED' : null);
    if (!key) return;
    let data: string;
    try {
      data = target.encodeKey({
        action: 'release',
        key,
        mods: modifiers(event),
        consumedMods: 0,
        composing: false,
        unshiftedCodepoint: unshiftedCodepoint(event, text),
      });
    } catch (error) {
      consumeBrowserEvent(event);
      onError('key', error);
      return;
    }
    if (data) {
      consumeBrowserEvent(event);
      send(data);
    }
  };

  const paste = (event: ClipboardEvent) => {
    if (disposed || event.defaultPrevented) return;
    const text = event.clipboardData?.getData('text/plain') ?? '';
    if (!text) return;
    consumeBrowserEvent(event);
    const target = terminal();
    if (!target) return;
    let data: string;
    try {
      data = target.formatPaste(text);
    } catch (error) {
      onError('paste', error);
      return;
    }
    send(data);
  };

  const compositionstart = () => {
    if (!disposed) composing = true;
  };
  const compositionupdate = () => undefined;
  const compositionend = (event: CompositionEvent) => {
    if (disposed) return;
    composing = false;
    if (event.data) send(event.data);
    removeCompositionTextNodes(element);
  };

  element.addEventListener('keydown', keydown);
  element.addEventListener('keyup', keyup);
  element.addEventListener('paste', paste);
  element.addEventListener('compositionstart', compositionstart);
  element.addEventListener('compositionupdate', compositionupdate);
  element.addEventListener('compositionend', compositionend);

  return () => {
    if (disposed) return;
    disposed = true;
    forwarded.clear();
    element.removeEventListener('keydown', keydown);
    element.removeEventListener('keyup', keyup);
    element.removeEventListener('paste', paste);
    element.removeEventListener('compositionstart', compositionstart);
    element.removeEventListener('compositionupdate', compositionupdate);
    element.removeEventListener('compositionend', compositionend);
  };
}
