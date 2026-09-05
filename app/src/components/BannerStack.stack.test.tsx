import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { BannerStack } from './BannerStack';

interface Observed {
  callback: (entries: Array<{ target: HTMLElement }>) => void;
  element: HTMLElement;
}

describe('BannerStack', () => {
  const observed: Observed[] = [];
  const openWarningUrl = vi.fn();

  afterEach(() => {
    observed.length = 0;
    openWarningUrl.mockClear();
    vi.unstubAllGlobals();
  });

  function installObserver() {
    vi.stubGlobal(
      'ResizeObserver',
      class {
        callback: Observed['callback'];
        constructor(callback: Observed['callback']) {
          this.callback = callback;
        }
        observe(element: HTMLElement) {
          observed.push({ callback: this.callback, element });
        }
        unobserve() {}
        disconnect() {}
      },
    );
  }

  function stubHeight(element: HTMLElement, height: number) {
    Object.defineProperty(element, 'offsetHeight', { value: height, configurable: true });
  }

  function measure(heights: number[]) {
    act(() => {
      for (const { callback, element } of observed.splice(0)) {
        const height = heights.shift();
        if (height != null) stubHeight(element, height);
        callback([{ target: element }]);
      }
    });
  }

  function updateBanner(): HTMLElement | null {
    return screen.queryByText(/is available on GitHub/)?.closest('div') ?? null;
  }

  it('tops the update banner with the measured warning height', () => {
    installObserver();
    render(
      <BannerStack
        connectionError={null}
        warnings={[{ code: 'gh_not_installed', message: 'GitHub CLI not installed.' }]}
        updateAvailableVersion="9.9.9"
        onOpenWarningUrl={openWarningUrl}
        onClearWarnings={() => {}}
        onOpenLatestRelease={() => Promise.resolve()}
        onDismissLatestRelease={() => {}}
      />,
    );
    measure([46]);
    expect(updateBanner()?.style.top).toBe('46px');
  });

  it('adds the measured connection height to both banners', () => {
    installObserver();
    render(
      <BannerStack
        connectionError="Daemon is recovering"
        warnings={[{ code: 'gh_version_too_old', message: 'GitHub CLI needs upgrade.' }]}
        updateAvailableVersion="9.9.9"
        onOpenWarningUrl={openWarningUrl}
        onClearWarnings={() => {}}
        onOpenLatestRelease={() => Promise.resolve()}
        onDismissLatestRelease={() => {}}
      />,
    );
    measure([48, 46]);
    const warning = screen.getByText('GitHub CLI needs upgrade.').closest('div');
    expect(warning?.style.top).toBe('48px');
    expect(updateBanner()?.style.top).toBe('94px');
  });

  it('leaves offsets to CSS until measurement runs', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    render(
      <BannerStack
        connectionError={null}
        warnings={[{ code: 'gh_not_installed', message: 'GitHub CLI not installed.' }]}
        updateAvailableVersion="9.9.9"
        onOpenWarningUrl={openWarningUrl}
        onClearWarnings={() => {}}
        onOpenLatestRelease={() => Promise.resolve()}
        onDismissLatestRelease={() => {}}
      />,
    );
    expect(updateBanner()?.style.top).toBe('');
  });
});
