import { describe, expect, it } from 'vitest';
import { DEFAULT_DOCK, parseKeybindingsConfig, serializeKeybindingsConfig, type KeybindingsConfig } from './resolver';

const CHORD = { leader: { key: 'k', meta: true }, then: { key: 'd' } };

describe('parseKeybindingsConfig', () => {
  it.each<[string, string | undefined, Partial<KeybindingsConfig>]>([
    ['nothing saved', undefined, { overrides: {}, dock: DEFAULT_DOCK }],
    ['text that is not JSON', 'not json', { overrides: {}, dock: DEFAULT_DOCK }],
    ['JSON that is not an object', '123', { overrides: {}, dock: DEFAULT_DOCK }],
    [
      'known and unknown ids',
      JSON.stringify({ version: 1, overrides: { 'session.new': { key: 'm', meta: true }, 'bogus.id': { key: 'x', meta: true }, 'app.quit': null } }),
      { overrides: { 'session.new': { key: 'm', meta: true }, 'app.quit': null } },
    ],
    ['an override without a key', JSON.stringify({ overrides: { 'session.new': { meta: true } } }), { overrides: {} }],
    ['a chord', JSON.stringify({ overrides: { 'dock.attention': CHORD } }), { overrides: { 'dock.attention': CHORD } }],
    ['a chord without a follow key', JSON.stringify({ overrides: { 'dock.attention': { leader: { key: 'k', meta: true } } } }), { overrides: {} }],
    ['overrides without a dock', JSON.stringify({ overrides: {} }), { dock: DEFAULT_DOCK }],
    ['a dock that is not an object', JSON.stringify({ dock: 'nope' }), { dock: DEFAULT_DOCK }],
    ['a dock whose items are not a list', JSON.stringify({ dock: { items: 5 } }), { dock: DEFAULT_DOCK }],
    [
      'a dock with unknown and repeated ids',
      JSON.stringify({ dock: { collapsed: true, items: ['dock.attention', 'bogus.id', 'dock.attention', 'session.new'] } }),
      { dock: { collapsed: true, items: ['dock.attention', 'session.new'] } },
    ],
    ['a dock collapsed flag that is not a boolean', JSON.stringify({ dock: { collapsed: 'yes', items: [] } }), { dock: { collapsed: false, items: [] } }],
  ])('reads %s', (_, raw, expected) => {
    const config = parseKeybindingsConfig(raw);
    for (const [field, value] of Object.entries(expected)) {
      expect(config[field as keyof KeybindingsConfig]).toEqual(value);
    }
  });

  it('reads back what it saved', () => {
    const config: KeybindingsConfig = {
      version: 1,
      overrides: { 'session.new': { key: 'm', meta: true }, 'app.quit': null, 'dock.attention': CHORD },
      dock: { collapsed: true, items: ['dock.attention', 'session.toggleSidebar'] },
    };

    expect(parseKeybindingsConfig(serializeKeybindingsConfig(config))).toEqual(config);
  });
});
