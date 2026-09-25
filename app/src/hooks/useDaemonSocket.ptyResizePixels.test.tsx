import { act } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ptyResize } from '../pty/bridge';
import { renderWithDaemon } from '../test/renderApp';

describe('useDaemonSocket pty_resize pixel geometry', () => {
  it('carries the pane total a fit measured', async () => {
    const { daemon } = await renderWithDaemon();

    await act(() => ptyResize({ id: 'sess-1', cols: 40, rows: 12, reason: 'ghostty_fit', xpixel: 720, ypixel: 540 }));

    expect(await daemon.received('pty_resize')).toEqual({
      cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12, xpixel: 720, ypixel: 540,
    });
  });

  it('omits the fields entirely when the resize measured nothing', async () => {
    const { daemon } = await renderWithDaemon();

    await act(() => ptyResize({ id: 'sess-1', cols: 40, rows: 12, reason: 'daemon_known_attach' }));

    expect(await daemon.received('pty_resize')).toEqual({ cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12 });
  });

  it('omits a half-measured pane rather than reporting one axis', async () => {
    const { daemon } = await renderWithDaemon();

    await act(() => ptyResize({ id: 'sess-1', cols: 40, rows: 12, xpixel: 720, ypixel: 0 }));

    expect(await daemon.received('pty_resize')).toEqual({ cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12 });
  });
});
