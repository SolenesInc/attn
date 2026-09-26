// @vitest-environment node
// @ts-expect-error -- @types/node is only a transitive peer here (see kittyWireRewrite.parity.test.ts)
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import { CellFlags, Ghostty, type GhosttyCell, type GhosttyTerminal } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

let ghostty: Ghostty;

beforeAll(async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  ghostty = new Ghostty(instance);
});

type Step = string | { resize: [number, number] };

interface Screen {
  cols?: number;
  rows?: number;
  config?: Parameters<Ghostty['createTerminal']>[2];
}

function rowText(t: GhosttyTerminal, y: number): string {
  const cells = t.getViewport();
  let text = '';
  for (let x = 0; x < t.cols; x += 1) {
    const cell = cells[y * t.cols + x];
    text += cell.codepoint ? String.fromCodePoint(cell.codepoint) : ' ';
  }
  return text.trimEnd();
}

function hex(r: number, g: number, b: number): string {
  return `#${[r, g, b].map((channel) => channel.toString(16).padStart(2, '0')).join('')}`;
}

function colors(cell: GhosttyCell): string {
  return `${hex(cell.fg_r, cell.fg_g, cell.fg_b)} on ${hex(cell.bg_r, cell.bg_g, cell.bg_b)}`;
}

const THEMED = { fgColor: 0x445566, bgColor: 0x112233 };
const THEME = '#445566 on #112233';
const lines = (count: number, prefix: string) => Array.from({ length: count }, (_, i) => `${prefix}${i}\r\n`);
const paletteWith = (entries: Record<number, number>) => Array.from({ length: 16 }, (_, index) => entries[index] ?? 0);

describe('GhosttyTerminal reads what libghostty-vt holds', () => {
  it.each<[string, Screen, Step[], (t: GhosttyTerminal) => unknown, unknown]>([
    ['text by viewport row', {}, ['hello\r\nworld'], (t) => [rowText(t, 0), rowText(t, 1)], ['hello', 'world']],
    ['every SGR attribute as a cell flag', {}, ['\x1b[1mB\x1b[0m\x1b[3mI\x1b[0m\x1b[4mU\x1b[0m\x1b[9mS\x1b[0m\x1b[7mV\x1b[0m\x1b[8mH\x1b[0m\x1b[5mK\x1b[0m\x1b[2mF\x1b[0m'],
      (t) => t.getViewport().slice(0, 8).map((cell) => cell.flags),
      [CellFlags.BOLD, CellFlags.ITALIC, CellFlags.UNDERLINE, CellFlags.STRIKETHROUGH, CellFlags.INVERSE, CellFlags.INVISIBLE, CellFlags.BLINK, CellFlags.FAINT]],
    ['theme defaults and true colors', { config: THEMED }, ['a\x1b[38;2;10;20;30m\x1b[48;2;40;50;60mb'],
      (t) => t.getViewport().slice(0, 2).map(colors), [THEME, '#0a141e on #28323c']],
    ['a themed ANSI color beside the extended palette', { config: { ...THEMED, palette: paletteWith({ 3: 0x123456 }) } }, ['\x1b[33mA\x1b[38;5;46mG\x1b[38;5;196mR\x1b[38;5;231mW\x1b[48;5;21mB'],
      (t) => t.getViewport().slice(0, 5).map(colors),
      ['#123456 on #112233', '#00ff00 on #112233', '#ff0000 on #112233', '#ffffff on #112233', '#ffffff on #0000ff']],
    ['scrollback colors from the style slots', { rows: 2, config: { ...THEMED, palette: paletteWith({ 1: 0xff0102, 9: 0x0a0b0c, 3: 0x030303, 2: 0x020202 }) } },
      ['filler-one\r\n', 'filler-two\r\n', '\x1b[31mred\x1b[0m\r\n', '\x1b[38;2;10;20;30mtcolor\x1b[0m\r\n', '\x1b[91mbright\x1b[0m\r\n', '\x1b[42mbgonly\x1b[0m\r\n', '\x1b[1;33mboldyl\x1b[0m\r\n', 'end\r\n'],
      (t) => [t.getScrollbackLength(), ...[0, 2, 3, 4, 5, 6].map((row) => {
        const cell = t.getScrollbackLine(row)![0];
        return `${String.fromCodePoint(cell.codepoint)} ${colors(cell)} ${cell.flags}`;
      })],
      [7, `f ${THEME} 0`, 'r #ff0102 on #112233 0', 't #0a141e on #112233 0', 'b #0a0b0c on #112233 0', 'b #445566 on #020202 0', `b #030303 on #112233 ${CellFlags.BOLD}`]],
    ['a wide cell and its spacer', {}, ['漢x'], (t) => t.getViewport().slice(0, 3).map((cell) => [cell.width, cell.codepoint]), [[2, '漢'.codePointAt(0)], [0, 0], [1, 'x'.codePointAt(0)]]],
    ['a combining cluster as one grapheme', {}, ['\x1b[?2027h', 'e\u0301'], (t) => [t.getViewport()[0].grapheme_len, t.getGraphemeString(0, 0)], [1, 'e\u0301']],
    ['the cursor and the theme colors', { config: THEMED }, ['abc'], (t) => [t.getCursor(), t.getColors()], [
      expect.objectContaining({ x: 3, y: 0, visible: true, style: 'block' }),
      expect.objectContaining({ foreground: { r: 0x44, g: 0x55, b: 0x66 }, background: { r: 0x11, g: 0x22, b: 0x33 }, cursor: null }),
    ]],
    ['a DSR reply, once', {}, ['\x1b[6n'], (t) => [t.hasResponse(), t.readResponse(), t.hasResponse(), t.readResponse()], [true, '\x1b[1;1R', false, null]],
    ['default DEC modes', {}, [], (t) => [t.getMode(7), t.hasBracketedPaste(), t.isAlternateScreen(), t.hasMouseTracking()], [true, false, false, false]],
    ['DEC modes the app gates behavior on', {}, ['\x1b[?2004h\x1b[?1006h\x1b[?7l'], (t) => [t.hasBracketedPaste(), t.getMode(1006), t.getMode(7)], [true, true, false]],
    ['the alternate screen and mouse tracking', {}, ['\x1b[?1049h\x1b[?1002h'], (t) => [t.isAlternateScreen(), t.hasMouseTracking()], [true, true]],
    ['leaving the alternate screen and mouse tracking', {}, ['\x1b[?1049h\x1b[?1002h', '\x1b[?1049l\x1b[?1002l'], (t) => [t.isAlternateScreen(), t.hasMouseTracking()], [false, false]],
    ['scalar state after a wide read', { config: { scrollbackLimit: 1 << 20 } }, [...lines(40, 'line'), '\x1b[?7h'],
      (t) => [t.rowWrapsIntoNext(1), t.hasMouseTracking(), t.isAlternateScreen(), t.getMode(7), t.getScrollbackLength()], [false, false, false, true, 36]],
    ['mouse tracking read right after a wrap query', { config: { scrollbackLimit: 1 << 20 } }, [...lines(40, 'line'), '\x1b[?1002h'],
      (t) => [t.rowWrapsIntoNext(1), t.hasMouseTracking()], [false, true]],
    ['scrollback rows by history offset', { rows: 3, config: { scrollbackLimit: 1 << 20 } }, lines(10, 'row'),
      (t) => [t.getScrollbackLength(), t.getScrollbackLine(0)!.slice(0, 4).map((cell) => String.fromCodePoint(cell.codepoint)).join(''), t.getScrollbackGraphemeString(0, 0), t.getScrollbackLine(8)],
      [8, 'row0', 'r', null]],
    ['a soft wrap on the row it starts from', { cols: 10 }, ['0123456789abc'], (t) => [t.rowWrapsIntoNext(0), t.rowWrapsIntoNext(1)], [true, false]],
    ['an OSC 8 hyperlink by position', {}, ['\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ x'],
      (t) => [t.getViewport()[0].hyperlink_id, t.getHyperlinkUri(0, 0), t.getViewport()[6].hyperlink_id, t.getHyperlinkUri(0, 6)], [1, 'https://example.com', 0, null]],
    ['text reflowed by a wider resize', { cols: 10 }, ['0123456789abcde', { resize: [20, 5] }], (t) => [t.cols, rowText(t, 0)], [20, '0123456789abcde']],
  ])('%s', (_name, screen, steps, read, expected) => {
    const t = ghostty.createTerminal(screen.cols ?? 20, screen.rows ?? 5, screen.config ?? {});
    for (const step of steps) {
      if (typeof step === 'string') t.write(step);
      else t.resize(...step.resize);
    }
    t.update();

    expect(read(t)).toEqual(expected);
    t.free();
  });
});
