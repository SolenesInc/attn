import { beforeEach, describe, expect, it } from 'vitest';
import {
  persistDesktopSelectionStyle,
  readDesktopSelectionStyle,
  DESKTOP_SELECTION_STYLE_STORAGE_KEY,
} from './desktopSelectionStyle';

describe('desktop selection style preference', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('defaults missing and unknown values to the spotlight', () => {
    expect(readDesktopSelectionStyle()).toBe('spotlight');

    localStorage.setItem(DESKTOP_SELECTION_STYLE_STORAGE_KEY, 'unknown');
    expect(readDesktopSelectionStyle()).toBe('spotlight');
  });

  it.each(['dim', 'rail'] as const)('persists and restores the %s style', (style) => {
    persistDesktopSelectionStyle(style);

    expect(localStorage.getItem(DESKTOP_SELECTION_STYLE_STORAGE_KEY)).toBe(style);
    expect(readDesktopSelectionStyle()).toBe(style);
  });
});
