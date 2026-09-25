import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen } from '@testing-library/react';
import { AppViewLoadError } from './loadAppView';
import { openDockedApprovals, reviewerApp, SERVING_HASH } from './testSupport';
import { gesture } from '../../test/renderApp';

const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

const HASH_B = 'b'.repeat(64);

beforeEach(() => {
  loadAppView.mockReset();
});

describe('a view that mounts', () => {
  it('is given where it sits and what the user typed when docking', async () => {
    const seen: Record<string, unknown>[] = [];
    loadAppView.mockResolvedValue((props: Record<string, unknown>) => {
      seen.push(props);
      return <div>approvals body</div>;
    });

    await openDockedApprovals([reviewerApp()]);

    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(seen[0]).toEqual({
      workspaceId: 'ws-1',
      sessionId: 'sess-1',
      tileId: 'tile-7',
      params: 't-42',
    });
  });
});

describe('a view that cannot mount', () => {
  it('says an uninstalled app is gone and leaves the tile where it is', async () => {
    await openDockedApprovals([]);
    expect(screen.getByText(/is not installed/).textContent).toContain('reviewer');
    expect(screen.getByText(/stays where you put it/)).toBeInTheDocument();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('names the command that turns a disabled app back on', async () => {
    await openDockedApprovals([reviewerApp({ enabled: false })]);
    expect(screen.getByText(/reviewer is disabled/)).toBeInTheDocument();
    expect(screen.getByText(/attn app enable reviewer/)).toBeInTheDocument();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('lists what the serving version does offer when the view is gone', async () => {
    await openDockedApprovals([reviewerApp({ views: [{ name: 'history', kind: 'tile', title: 'History' }] })]);
    expect(screen.getByText(/no longer has a view called/)).toBeInTheDocument();
    expect(screen.getByText(/offers: history/)).toBeInTheDocument();
  });

  it('offers Retry when the bundle will not load, and retries on click', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError('This view could not be loaded.', 'Importing failed: boom'));
    const daemon = await openDockedApprovals([reviewerApp()]);

    expect(screen.getByText('This view could not be loaded.')).toBeInTheDocument();
    expect(screen.getByText(/Importing failed: boom/)).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })));
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(loadAppView.mock.calls[1][0]).not.toBe(loadAppView.mock.calls[0][0]);
    expect(loadAppView.mock.calls[1][0]).toContain(SERVING_HASH);
  });

  it('names the binding when the module exports no component', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError(
      'This view exports no component.',
      'It must export a React component as its default export. It exports: Approvals.',
    ));
    await openDockedApprovals([reviewerApp()]);
    expect(screen.getByText('This view exports no component.')).toBeInTheDocument();
    expect(screen.getByText(/It exports: Approvals/)).toBeInTheDocument();
  });
});

describe('a view that throws while rendering', () => {
  it('costs its own tile and is reported against the serving version', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    loadAppView.mockResolvedValue(() => {
      throw new Error('cannot read properties of undefined');
    });

    const daemon = await openDockedApprovals([reviewerApp()]);

    expect(screen.getByText(/crashed while rendering/)).toBeInTheDocument();
    expect(screen.getByText(/attn app logs reviewer/)).toBeInTheDocument();
    expect(document.querySelector('[data-pane-id="pane-sess-1"]')).toBeInTheDocument();
    const reports = daemon.sentOf('app_view_crash');
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ app: 'reviewer', view: 'approvals', version_id: 7, tile_id: 'tile-7' });
    expect(reports[0].error).toContain('cannot read properties of undefined');
    consoleError.mockRestore();
  });

  it('drops the crashed component on Reload rather than reporting the same crash again', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    loadAppView.mockResolvedValue(() => {
      throw new Error('cannot read properties of undefined');
    });

    const daemon = await openDockedApprovals([reviewerApp()]);
    expect(screen.getByText(/crashed while rendering/)).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);

    let resolveSecond: (component: unknown) => void = () => {};
    loadAppView.mockReturnValue(new Promise((resolve) => { resolveSecond = resolve; }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reload' })));

    expect(screen.getByText(/Loading reviewer\/approvals/)).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);

    await gesture(daemon, () => resolveSecond(() => <div>approvals body</div>));
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);
    consoleError.mockRestore();
  });
});

describe('a version that moves under a docked tile', () => {
  it('remounts against the new bundle when the tile does not hold focus', async () => {
    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    const daemon = await openDockedApprovals([reviewerApp()]);
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    await gesture(daemon, () => daemon.emit({ event: 'apps_updated', apps: [reviewerApp({ version_id: 8, content_hash: HASH_B })] }));

    expect(loadAppView).toHaveBeenCalledTimes(2);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });

  it('waits for the user to leave rather than pulling the view out mid-keystroke', async () => {
    loadAppView.mockResolvedValue(() => <input aria-label="app input" />);
    const daemon = await openDockedApprovals([reviewerApp()]);

    await gesture(daemon, () => act(() => screen.getByLabelText('app input').focus()));
    await gesture(daemon, () => daemon.emit({ event: 'apps_updated', apps: [reviewerApp({ version_id: 8, content_hash: HASH_B })] }));

    expect(screen.getByText(/reloading when you leave this tile/)).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    await gesture(daemon, () => act(() => screen.getByRole('button', { name: 'Open reviewing' }).focus()));
    expect(loadAppView).toHaveBeenCalledTimes(2);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });
});
