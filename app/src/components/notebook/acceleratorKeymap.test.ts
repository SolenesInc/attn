import { describe, it, expect } from 'vitest';
import { acceleratorBindings } from './acceleratorKeymap';
import { withNavigatorPlatform } from '../../test/platformStub';

const run = () => true;

describe('acceleratorBindings', () => {
  it('pins Mod- to Cmd- on macOS', () => {
    withNavigatorPlatform('MacIntel', () => {
      expect(acceleratorBindings([{ key: 'Mod-b', run }]).map((b) => b.key)).toEqual(['Cmd-b']);
    });
  });

  it('adds a Ctrl- binding beside Cmd- off-mac', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(acceleratorBindings([{ key: 'Mod-f', run }]).map((b) => b.key))
        .toEqual(['Cmd-f', 'Ctrl-f']);
      expect(acceleratorBindings([{ key: 'Cmd-e', run }]).map((b) => b.key))
        .toEqual(['Cmd-e', 'Ctrl-e']);
    });
  });

  it('passes bindings that carry no accelerator through untouched', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(acceleratorBindings([{ key: 'Escape', run }, { run }]).map((b) => b.key))
        .toEqual(['Escape', undefined]);
    });
  });
});
