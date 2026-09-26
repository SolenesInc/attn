import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonEndpoint } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { CommandMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const HOME = '/Users/me';

type Directories = Record<string, string[]>;

function browseAnswers(daemon: ScriptedDaemon, byEndpoint: Record<string, Directories | 'hold'>) {
  daemon.on('browse_directory', ({ input_path, endpoint_id }) => {
    const directories = byEndpoint[endpoint_id ?? 'local'];
    if (directories === 'hold') return undefined;
    const names = directories?.[input_path] ?? [];
    const directory = input_path.replace(/^~/, HOME).replace(/\/$/, '');
    return {
      event: 'browse_directory_result',
      success: true,
      input_path,
      directory,
      home_path: HOME,
      entries: names.map((name) => ({ name, path: `${directory}/${name}`, is_dir: true })),
    };
  });
}

async function openPicker(script: (daemon: ScriptedDaemon) => void = () => {}) {
  const view = await renderApp({
    initialState: {
      endpoints: [daemonEndpoint('ep-1')],
    },
  });
  script(view.daemon);
  await gesture(view.daemon, () => pressShortcut('session.new'));
  const input = screen.getByTestId('location-picker-path-input');
  const type = (value: string) => fireEvent.change(input, { target: { value } });
  const settle = async (ms = 0) => {
    await act(() => vi.advanceTimersByTimeAsync(ms));
    await view.daemon.idle();
  };
  return { ...view, input, type, settle };
}

function browses(daemon: ScriptedDaemon) {
  return daemon.sentOf('browse_directory').map(({ input_path, endpoint_id }: CommandMessage<'browse_directory'>) => (
    endpoint_id ? { input_path, endpoint_id } : { input_path }
  ));
}

function suggestions() {
  return Array.from(document.querySelectorAll('.picker-item .picker-name'), (name) => name.textContent);
}

describe('App location picker', () => {
  it('asks the host the session will run on, and never shows another host’s directories', async () => {
    const view = await openPicker((daemon) => browseAnswers(daemon, {
      local: { '~/projects/': ['local-repo'] },
      'ep-1': 'hold',
    }));
    view.type('~/projects/');
    await view.settle(150);
    expect(suggestions()).toContain('local-repo');

    await gesture(view.daemon, () => fireEvent.click(screen.getByTestId('location-picker-target-ep-1')));
    expect(suggestions()).not.toContain('local-repo');
    const [remote] = view.daemon.sentOf('browse_directory').slice(-1);
    expect(remote.endpoint_id).toBe('ep-1');

    view.daemon.replyTo(remote, {
      event: 'browse_directory_result',
      request_id: remote.request_id,
      success: true,
      input_path: remote.input_path,
      directory: '/home/me',
      home_path: '/home/me',
      entries: [{ name: 'remote-repo', path: '/home/me/remote-repo', is_dir: true }],
    });
    await view.settle();
    expect(suggestions()).toContain('remote-repo');
    expect(suggestions()).not.toContain('local-repo');
  });

  it('asks once for the path the user settles on, not for every keystroke', async () => {
    const view = await openPicker((daemon) => browseAnswers(daemon, { local: {} }));
    view.type('~/projects/');
    await view.settle(150);

    for (const path of ['~/projects/a', '~/projects/at', '~/projects/att', '~/projects/attn']) {
      view.type(path);
      await view.settle(50);
    }
    await view.settle(150);

    expect(browses(view.daemon)).toEqual([{ input_path: '~/projects/' }, { input_path: '~/projects/attn' }]);
  });

  it('stops browsing once the picker closes', async () => {
    const view = await openPicker((daemon) => browseAnswers(daemon, { local: { '~/projects/': ['attn'] } }));
    view.type('~/projects/');
    await view.settle(150);
    expect(suggestions()).toContain('attn');

    view.type('~/projects/attn');
    await gesture(view.daemon, () => fireEvent.keyDown(view.input, { key: 'Escape' }));
    await view.settle(500);

    expect(screen.queryByTestId('location-picker-overlay')).toBeNull();
    expect(browses(view.daemon)).toEqual([{ input_path: '~/projects/' }]);
  });

  it.each([
    ['/tmp/project/', '/tmp/project'],
    ['/tmp/project', '/tmp/project'],
    ['~/', HOME],
    ['/', '/'],
  ])('opens %s as %s', async (typed, path) => {
    const view = await openPicker((daemon) => browseAnswers(daemon, { local: {} }));
    view.type('~/');
    await view.settle(150);

    view.type(typed);
    await view.settle(150);
    await gesture(view.daemon, () => fireEvent.keyDown(view.input, { key: 'Enter' }));

    expect(view.daemon.sentOf('inspect_path').map((command) => command.path)).toEqual([path]);
  });
});
