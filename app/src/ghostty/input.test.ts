import { Window } from 'happy-dom';
import { describe, expect, it, vi } from 'vitest';
import {
  attachTerminalInput,
  type TerminalInputTarget,
} from './input';
import type { TerminalKeyEvent } from './keyEncoder';
import { withNavigatorPlatform } from '../test/platformStub';

function setup(overrides: Partial<{
  target: TerminalInputTarget | null;
  interceptKey: (event: KeyboardEvent) => boolean;
}> = {}) {
  const window = new Window();
  const element = window.document.createElement('div') as unknown as HTMLElement;
  let target = overrides.target === undefined ? {
    encodeKey: vi.fn((event: TerminalKeyEvent) => event.action),
    formatPaste: vi.fn((text: string) => `<${text}>`),
  } : overrides.target;
  const send = vi.fn();
  const onError = vi.fn();
  const interceptKey = vi.fn(overrides.interceptKey ?? (() => false));
  const dispose = attachTerminalInput({
    element,
    terminal: () => target,
    send,
    interceptKey,
    onError,
  });
  return {
    window,
    element,
    get target() { return target; },
    setTarget(next: TerminalInputTarget | null) { target = next; },
    send,
    onError,
    interceptKey,
    dispose,
  };
}

function key(
  window: Window,
  type: 'keydown' | 'keyup',
  init: KeyboardEventInit,
  modifierStates: readonly string[] = [],
): KeyboardEvent {
  const event = new window.KeyboardEvent(type, {
    bubbles: true,
    cancelable: true,
    ...init,
  } as never) as unknown as KeyboardEvent;
  Object.defineProperty(event, 'getModifierState', {
    value: (name: string) => modifierStates.includes(name),
  });
  return event;
}

describe('attachTerminalInput', () => {
  it('routes physical key names and layout text through the target', () => {
    const input = setup();
    const arrow = key(input.window, 'keydown', { key: 'ArrowUp', code: 'ArrowUp' });
    input.element.dispatchEvent(arrow);
    const letter = key(input.window, 'keydown', {
      key: 'A',
      code: 'KeyA',
      shiftKey: true,
    });
    input.element.dispatchEvent(letter);

    expect(input.target!.encodeKey).toHaveBeenNthCalledWith(1, expect.objectContaining({
      action: 'press',
      key: 'ARROW_UP',
      utf8: undefined,
    }));
    expect(input.target!.encodeKey).toHaveBeenNthCalledWith(2, {
      action: 'press',
      key: 'A',
      mods: 1,
      consumedMods: 1,
      composing: false,
      utf8: 'A',
      unshiftedCodepoint: 97,
    });
    expect(input.send).toHaveBeenCalledWith('press');
    expect(arrow.defaultPrevented).toBe(true);
    expect(letter.defaultPrevented).toBe(true);
    input.dispose();
  });

  it('carries repeat, release, lock, AltGraph, and right-side modifiers', () => {
    const input = setup();
    const repeated = key(input.window, 'keydown', {
      key: '@',
      code: 'AltRight',
      repeat: true,
      ctrlKey: true,
      altKey: true,
      location: 2,
    }, ['AltGraph', 'CapsLock', 'NumLock']);
    input.element.dispatchEvent(repeated);
    input.element.dispatchEvent(key(input.window, 'keyup', {
      key: 'Alt',
      code: 'AltRight',
      location: 2,
    }));

    expect(input.target!.encodeKey).toHaveBeenNthCalledWith(1, expect.objectContaining({
      action: 'repeat',
      key: 'ALT_RIGHT',
      mods: 2 | 4 | 16 | 32 | 256,
      consumedMods: 2 | 4 | 16 | 32,
      utf8: '@',
    }));
    expect(input.target!.encodeKey).toHaveBeenNthCalledWith(2, expect.objectContaining({
      action: 'release',
      key: 'ALT_RIGHT',
      mods: 0,
    }));
    input.dispose();
  });

  it('treats macOS Option as terminal Alt instead of forwarding its composed glyph', () => {
    const input = setup();
    input.element.dispatchEvent(key(input.window, 'keydown', {
      key: 'ƒ',
      code: 'KeyF',
      altKey: true,
    }));

    expect(input.target!.encodeKey).toHaveBeenCalledWith({
      action: 'press',
      key: 'F',
      mods: 4,
      consumedMods: 0,
      composing: false,
      utf8: undefined,
      unshiftedCodepoint: 102,
    });
    input.dispose();
  });

  it('does not emit an orphan release for an attn shortcut', () => {
    const input = setup({ interceptKey: () => true });
    const down = key(input.window, 'keydown', { key: 'k', code: 'KeyK', metaKey: true });
    const up = key(input.window, 'keyup', { key: 'k', code: 'KeyK' });
    input.element.dispatchEvent(down);
    input.element.dispatchEvent(up);

    expect(down.defaultPrevented).toBe(true);
    expect(input.target!.encodeKey).not.toHaveBeenCalled();
    expect(input.send).not.toHaveBeenCalled();
    input.dispose();
  });

  it('leaves native browser copy and paste shortcuts alone', () => {
    const input = setup();
    const copy = key(input.window, 'keydown', { key: 'c', code: 'KeyC', metaKey: true });
    const paste = key(input.window, 'keydown', { key: 'v', code: 'KeyV', metaKey: true });
    input.element.dispatchEvent(copy);
    input.element.dispatchEvent(paste);

    expect(copy.defaultPrevented).toBe(false);
    expect(paste.defaultPrevented).toBe(false);
    expect(input.target!.encodeKey).not.toHaveBeenCalled();
    input.dispose();
  });

  it('off-mac sends Ctrl+C to the terminal and reserves Ctrl+Shift+C for copy', () => {
    const input = setup();
    withNavigatorPlatform('Linux aarch64', () => {
      const interrupt = key(input.window, 'keydown', { key: 'c', code: 'KeyC', ctrlKey: true });
      const copy = key(input.window, 'keydown', {
        key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true,
      });
      input.element.dispatchEvent(interrupt);
      input.element.dispatchEvent(copy);

      expect(input.target!.encodeKey).toHaveBeenCalledTimes(1);
      expect(input.target!.encodeKey).toHaveBeenCalledWith(
        expect.objectContaining({ key: 'C', mods: 2 }),
      );
      expect(copy.defaultPrevented).toBe(false);
    });
    input.dispose();
  });

  it('leaves the browser default alone when libghostty produces no bytes', () => {
    const target = {
      encodeKey: vi.fn(() => ''),
      formatPaste: vi.fn((text: string) => text),
    };
    const input = setup({ target });
    const event = key(input.window, 'keydown', { key: 'Alt', code: 'AltLeft', altKey: true });
    input.element.dispatchEvent(event);

    expect(target.encodeKey).toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
    expect(input.send).not.toHaveBeenCalled();
    input.dispose();
  });

  it('commits composition text once and removes browser text nodes', () => {
    const input = setup();
    const dead = key(input.window, 'keydown', {
      key: 'Dead',
      code: 'KeyU',
      altKey: true,
    });
    input.element.dispatchEvent(dead);
    input.element.dispatchEvent(new input.window.Event('compositionstart') as unknown as Event);
    input.element.dispatchEvent(key(input.window, 'keydown', { key: 'a', code: 'KeyA' }));
    input.element.appendChild(input.window.document.createTextNode('preedit') as unknown as Node);
    const end = new input.window.Event('compositionend') as unknown as CompositionEvent;
    Object.defineProperty(end, 'data', { value: 'å' });
    input.element.dispatchEvent(end);

    expect(input.target!.encodeKey).not.toHaveBeenCalled();
    expect(dead.defaultPrevented).toBe(false);
    expect(input.send).toHaveBeenCalledTimes(1);
    expect(input.send).toHaveBeenCalledWith('å');
    expect(input.element.childNodes).toHaveLength(0);
    input.dispose();
  });

  it('delegates browser paste formatting to the target terminal', () => {
    const input = setup();
    const paste = new input.window.Event('paste', {
      bubbles: true,
      cancelable: true,
    }) as unknown as ClipboardEvent;
    Object.defineProperty(paste, 'clipboardData', {
      value: { getData: () => 'one\r\ntwo\nthree' },
    });
    input.element.dispatchEvent(paste);

    expect(input.target!.formatPaste).toHaveBeenCalledWith('one\r\ntwo\nthree');
    expect(input.send).toHaveBeenCalledWith('<one\r\ntwo\nthree>');
    expect(paste.defaultPrevented).toBe(true);
    input.dispose();
  });

  it('does not release a key into a replacement terminal', () => {
    const first = {
      encodeKey: vi.fn((event: TerminalKeyEvent) => event.action),
      formatPaste: vi.fn((text: string) => text),
    };
    const second = {
      encodeKey: vi.fn((event: TerminalKeyEvent) => event.action),
      formatPaste: vi.fn((text: string) => text),
    };
    const input = setup({ target: first });
    input.element.dispatchEvent(key(input.window, 'keydown', { key: 'a', code: 'KeyA' }));
    input.setTarget(second);
    input.element.dispatchEvent(key(input.window, 'keyup', { key: 'a', code: 'KeyA' }));

    expect(first.encodeKey).toHaveBeenCalledTimes(1);
    expect(second.encodeKey).not.toHaveBeenCalled();
    input.dispose();
  });

  it('reports encoder failures without sending partial input', () => {
    const failure = new Error('encode failed');
    const target = {
      encodeKey: vi.fn(() => { throw failure; }),
      formatPaste: vi.fn((text: string) => text),
    };
    const input = setup({ target });
    const event = key(input.window, 'keydown', { key: 'ArrowDown', code: 'ArrowDown' });
    input.element.dispatchEvent(event);
    input.element.dispatchEvent(key(input.window, 'keyup', {
      key: 'ArrowDown',
      code: 'ArrowDown',
    }));

    expect(input.onError).toHaveBeenCalledWith('key', failure);
    expect(target.encodeKey).toHaveBeenCalledTimes(1);
    expect(input.send).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
    input.dispose();
  });

  it('swallows terminal keys with no grid target and removes every listener on dispose', () => {
    const input = setup({ target: null });
    const before = key(input.window, 'keydown', { key: 'ArrowLeft', code: 'ArrowLeft' });
    input.element.dispatchEvent(before);
    expect(before.defaultPrevented).toBe(true);

    input.dispose();
    input.dispose();
    const after = key(input.window, 'keydown', { key: 'ArrowRight', code: 'ArrowRight' });
    input.element.dispatchEvent(after);
    expect(after.defaultPrevented).toBe(false);
    expect(input.send).not.toHaveBeenCalled();
  });
});
