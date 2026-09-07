import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useKeyboardShortcuts } from './useKeyboardShortcuts';

const mockUseShortcut = vi.fn();

vi.mock('../shortcuts/useShortcut', () => ({
  useShortcut: (...args: unknown[]) => mockUseShortcut(...args),
}));

vi.mock('../shortcuts/platform', () => ({
  isAccelKeyPressed: () => false,
  isMacLikePlatform: () => true,
}));

function options(enabled = true) {
  return {
    onNewSession: vi.fn(),
    onCloseSession: vi.fn(),
    onToggleActionMenu: vi.fn(),
    onGoToDashboard: vi.fn(),
    onJumpToWaiting: vi.fn(),
    onSelectWorkspaceByIndex: vi.fn(),
    onPrevSession: vi.fn(),
    onNextSession: vi.fn(),
    onHistoryBack: vi.fn(),
    onHistoryForward: vi.fn(),
    enabled,
  };
}

describe('useKeyboardShortcuts', () => {
  beforeEach(() => mockUseShortcut.mockClear());

  it('registers both agent history actions with the app enabled state', () => {
    const config = options();
    renderHook(() => useKeyboardShortcuts(config));

    expect(mockUseShortcut).toHaveBeenCalledWith(
      'session.historyBack', config.onHistoryBack, true,
    );
    expect(mockUseShortcut).toHaveBeenCalledWith(
      'session.historyForward', config.onHistoryForward, true,
    );
  });

  it('disables history actions with the rest of app session shortcuts', () => {
    const config = options(false);
    renderHook(() => useKeyboardShortcuts(config));

    expect(mockUseShortcut).toHaveBeenCalledWith(
      'session.historyBack', config.onHistoryBack, false,
    );
    expect(mockUseShortcut).toHaveBeenCalledWith(
      'session.historyForward', config.onHistoryForward, false,
    );
  });
});
