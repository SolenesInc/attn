// @vitest-environment node
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { Ghostty, type GhosttyCell, type GhosttyTerminal } from '../ghostty';
import { beforeAll, describe, expect, it } from 'vitest';
import { KittyPlacementStore } from './kittyPlacements';
import type { PlacementElement } from '../types/generated';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

async function loadGhostty(): Promise<Ghostty> {
  const bytes = readFileSync(wasmPath);
  const mod = await WebAssembly.compile(bytes);
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  return new Ghostty(instance);
}

function rowText(cells: GhosttyCell[] | null): string {
  if (!cells) return '';
  let text = '';
  for (const cell of cells) {
    if (cell.width === 0) continue;
    text += cell.codepoint === 0 ? ' ' : String.fromCodePoint(cell.codepoint);
  }
  return text.replace(/ +$/, '');
}

function textAtBufferRow(term: GhosttyTerminal, bufferRow: number): string {
  const history = term.getScrollbackLength();
  return bufferRow < history
    ? rowText(term.getScrollbackLine(bufferRow))
    : rowText(term.getLine(bufferRow - history));
}

function placement(overrides: Partial<PlacementElement> = {}): PlacementElement {
  return {
    image_id: 1,
    placement_id: 1,
    image_generation: 10,
    virtual: false,
    z: 0,
    viewport_row: 0,
    viewport_col: 0,
    viewport_visible: true,
    grid_cols: 0,
    grid_rows: 0,
    pixel_width: 64,
    pixel_height: 64,
    source_x: 0,
    source_y: 0,
    source_width: 0,
    source_height: 0,
    ...overrides,
  };
}

type Call =
  | ['write', string]
  | ['apply', number, PlacementElement[]]
  | ['seed', PlacementElement[]];

describe('KittyPlacementStore', () => {
  let ghostty: Ghostty;

  beforeAll(async () => {
    ghostty = await loadGhostty();
  });

  function scrolledTerminal(): GhosttyTerminal {
    const term = ghostty.createTerminal(40, 10, { scrollbackLimit: 10000 });
    term.write(new TextEncoder().encode(Array.from({ length: 40 }, (_, i) => `line ${i}`).join('\r\n')));
    term.update();
    expect(term.getScrollbackLength()).toBe(30);
    return term;
  }

  it.each<[string, Call[], boolean[], [number, string][]]>([
    ['anchors a placement to the row the worker put it on', [['apply', 1, [placement({ viewport_row: 3 })]]], [true], [[1, 'line 33']]],
    ['anchors a placement that scrolled off the top into the scrollback, even when reported invisible', [['apply', 1, [placement({ viewport_row: -5, viewport_visible: false })]]], [true], [[1, 'line 25']]],
    ['culls a placement above the history the client still holds', [['apply', 1, [placement({ placement_id: 1, viewport_row: -31 }), placement({ placement_id: 2, viewport_row: -30 })]]], [true], [[2, 'line 0']]],
    ['re-maps a description against the scrollback of the moment', [['apply', 1, [placement({ viewport_row: 2 })]], ['write', '\r\nmore\r\nmore\r\nmore'], ['apply', 2, [placement({ viewport_row: 2 })]]], [true, true], [[1, 'line 35']]],
    ['rejects a set older than the one applied', [['apply', 5, [placement({ placement_id: 1 })]], ['apply', 4, [placement({ placement_id: 2 })]]], [true, false], [[1, 'line 30']]],
    ['accepts a set at the same seq, as a resize re-describes', [['apply', 5, [placement({ viewport_row: 0 })]], ['apply', 5, [placement({ viewport_row: 4 })]]], [true, true], [[1, 'line 34']]],
    ['replaces the set wholesale rather than merging', [['apply', 1, [placement({ placement_id: 1 }), placement({ placement_id: 2, viewport_row: 1 })]], ['apply', 2, [placement({ placement_id: 3, viewport_row: 2 })]]], [true, true], [[3, 'line 32']]],
    ['clears on the empty set', [['apply', 1, [placement()]], ['apply', 2, []]], [true, true], []],
    ['skips a virtual placement, which the program draws itself', [['apply', 1, [placement({ placement_id: 1, virtual: true }), placement({ placement_id: 2 })]]], [true], [[2, 'line 30']]],
    ['orders by z, then placement id', [['apply', 1, [placement({ placement_id: 9, z: 5 }), placement({ placement_id: 3, z: -1, viewport_row: 1 }), placement({ placement_id: 1, z: 5, viewport_row: 2 })]]], [true], [[3, 'line 31'], [1, 'line 32'], [9, 'line 30']]],
    ['drops what a restore does not carry, and accepts any seq after it', [['apply', 7, [placement()]], ['seed', []], ['apply', 1, [placement({ viewport_row: 1 })]]], [true, true], [[1, 'line 31']]],
    ['takes a restore snapshot as the whole truth', [['apply', 7, [placement({ placement_id: 1 })]], ['seed', [placement({ placement_id: 2, viewport_row: 1 })]]], [true], [[2, 'line 31']]],
  ])('%s', (_name, calls, accepted, placed) => {
    const term = scrolledTerminal();
    const store = new KittyPlacementStore();
    const results: boolean[] = [];
    for (const call of calls) {
      if (call[0] === 'write') {
        term.write(new TextEncoder().encode(call[1]));
        term.update();
      } else if (call[0] === 'apply') {
        results.push(store.apply(call[1], call[2], term.getScrollbackLength()));
      } else {
        store.seed(call[1], term.getScrollbackLength());
      }
    }

    expect(results).toEqual(accepted);
    expect(store.placements().map((p) => [p.placementId, textAtBufferRow(term, p.bufferRow)])).toEqual(placed);
  });
});
