import { it, expect } from 'vitest';
import { LINUX_SHORTCUTS, matchesShortcut } from './registry';
import { withNavigatorPlatform } from '../test/platformStub';

it('Ctrl+Shift+0 on Linux US layout matches ui.resetFontSize', () => {
  withNavigatorPlatform('Linux x86_64', () => {
    const e = new KeyboardEvent('keydown', { key: ')', code: 'Digit0', ctrlKey: true, shiftKey: true });
    expect(matchesShortcut(e, LINUX_SHORTCUTS['ui.resetFontSize'])).toBe(true);
  });
});
