import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { AppTileHost } from './AppTileHost';
import { AppViewLoadError } from './loadAppView';
import { renderWithDaemon } from '../../test/renderApp';
import type { EventMessage } from '../../test/protocol';

type AppEntry = EventMessage<'apps_updated'>['apps'][number];

const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

const HASH_A = 'a'.repeat(64);
const HASH_B = 'b'.repeat(64);

function entry(overrides: Partial<AppEntry> = {}): AppEntry {
  return {
    name: 'reviewer',
    enabled: true,
    version_id: 7,
    content_hash: HASH_A,
    views: [{ name: 'approvals', kind: 'tile', title: 'Pending approvals' }],
    ...overrides,
  };
}

async function renderHost(apps: AppEntry[]) {
  const view = await renderWithDaemon(
    <AppTileHost
      app="reviewer"
      view="approvals"
      workspaceId="ws-1"
      sessionId="sess-1"
      tileId="tile-7"
      params="t-42"
    />,
    { initialState: { apps } },
  );
  await view.daemon.idle();
  return view.daemon;
}

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

    await renderHost([entry()]);

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
    await renderHost([]);
    expect(screen.getByText(/is not installed/).textContent).toContain('reviewer');
    expect(screen.getByText(/stays where you put it/)).toBeInTheDocument();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('names the command that turns a disabled app back on', async () => {
    await renderHost([entry({ enabled: false })]);
    expect(screen.getByText(/reviewer is disabled/)).toBeInTheDocument();
    expect(screen.getByText(/attn app enable reviewer/)).toBeInTheDocument();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('lists what the serving version does offer when the view is gone', async () => {
    await renderHost([entry({ views: [{ name: 'history', kind: 'tile', title: 'History' }] })]);
    expect(screen.getByText(/no longer has a view called/)).toBeInTheDocument();
    expect(screen.getByText(/offers: history/)).toBeInTheDocument();
  });

  it('offers Retry when the bundle will not load, and retries on click', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError('This view could not be loaded.', 'Importing failed: boom'));
    const daemon = await renderHost([entry()]);

    expect(screen.getByText('This view could not be loaded.')).toBeInTheDocument();
    expect(screen.getByText(/Importing failed: boom/)).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await daemon.idle();
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(loadAppView.mock.calls[1][0]).not.toBe(loadAppView.mock.calls[0][0]);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_A);
  });

  it('names the binding when the module exports no component', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError(
      'This view exports no component.',
      'It must export a React component as its default export. It exports: Approvals.',
    ));
    await renderHost([entry()]);
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

    const daemon = await renderHost([entry()]);

    expect(screen.getByText(/crashed while rendering/)).toBeInTheDocument();
    expect(screen.getByText(/attn app logs reviewer/)).toBeInTheDocument();
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

    const daemon = await renderHost([entry()]);
    expect(screen.getByText(/crashed while rendering/)).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);

    let resolveSecond: (component: unknown) => void = () => {};
    loadAppView.mockReturnValue(new Promise((resolve) => { resolveSecond = resolve; }));
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    await daemon.idle();

    expect(screen.getByText(/Loading reviewer\/approvals/)).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);

    resolveSecond(() => <div>approvals body</div>);
    await daemon.idle();
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(daemon.sentOf('app_view_crash')).toHaveLength(1);
    consoleError.mockRestore();
  });
});

describe('a version that moves under a docked tile', () => {
  it('remounts against the new bundle when the tile does not hold focus', async () => {
    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    const daemon = await renderHost([entry()]);
    expect(screen.getByText('approvals body')).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    daemon.emit({ event: 'apps_updated', apps: [entry({ version_id: 8, content_hash: HASH_B })] });
    await daemon.idle();

    expect(loadAppView).toHaveBeenCalledTimes(2);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });

  it('waits for the user to leave rather than pulling the view out mid-keystroke', async () => {
    loadAppView.mockResolvedValue(() => <input aria-label="app input" />);
    const daemon = await renderHost([entry()]);
    const input = screen.getByLabelText('app input');

    fireEvent.focus(input);
    daemon.emit({ event: 'apps_updated', apps: [entry({ version_id: 8, content_hash: HASH_B })] });
    await daemon.idle();

    expect(screen.getByText(/reloading when you leave this tile/)).toBeInTheDocument();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    fireEvent.blur(input, { relatedTarget: document.body });
    await daemon.idle();
    expect(loadAppView).toHaveBeenCalledTimes(2);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });
});
