import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./keyEncoder', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./keyEncoder')>();
  return {
    ...actual,
    readGhosttyKeyAbi: () => ({
      keys: {},
      actions: {},
      encoderOptions: {},
      optionAsAlt: {},
    }),
    assertGhosttyKeyNames: () => undefined,
  };
});

import { loadGhostty, resetGhosttyModuleCacheForTests } from './wasm';

describe('loadGhostty', () => {
  beforeEach(() => {
    resetGhosttyModuleCacheForTests();
  });

  it('compiles once and creates a separate WASM instance per caller', async () => {
    const module = {} as WebAssembly.Module;
    const instances = [
      { exports: { memory: new WebAssembly.Memory({ initial: 1 }) } },
      { exports: { memory: new WebAssembly.Memory({ initial: 1 }) } },
    ] as WebAssembly.Instance[];
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      statusText: 'OK',
      arrayBuffer: async () => new ArrayBuffer(8),
    })));
    const compile = vi.spyOn(WebAssembly, 'compile').mockResolvedValue(module);
    const instantiate = vi.spyOn(WebAssembly, 'instantiate')
      .mockResolvedValueOnce(instances[0])
      .mockResolvedValueOnce(instances[1]);

    const runtimes = await Promise.all([loadGhostty(), loadGhostty()]);

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(compile).toHaveBeenCalledTimes(1);
    expect(instantiate).toHaveBeenCalledTimes(2);
    expect(runtimes.map((runtime) => runtime.exports.memory)).toEqual(
      instances.map((instance) => instance.exports.memory),
    );
    expect(runtimes).toHaveLength(2);
  });
});
