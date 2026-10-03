import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { QuickCapture } from './components/QuickCapture';
import { CAPTURE_READY, CAPTURE_REQUEST, type CaptureDraft } from './quickCapture/client';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { serveSettings } from './test/settings';
import type { EventMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const localFetch = globalThis.fetch;
const native = vi.hoisted(() => {
  (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
  return ({
  listeners: new Map<string, Set<(event: { payload: unknown }) => unknown>>(),
  draft: null as unknown,
  images: new Map<string, string>(),
  binding: null as string | null,
  active: null as string | null,
  onHide: null as (() => void) | null,
  onDrop: undefined as ((event: { payload: { type: string; paths: string[]; position: { x: number; y: number } } }) => unknown) | undefined,
}); });
vi.mock('@tauri-apps/api/event', () => {
  async function emitTo(_label: string, event: string, payload?: unknown) {
    for (const listener of native.listeners.get(event) ?? []) await listener({ payload });
  }
  return {
    emit: (event: string, payload?: unknown) => emitTo('', event, payload), emitTo,
    listen: async (event: string, listener: (event: { payload: unknown }) => unknown) => {
      const listeners = native.listeners.get(event) ?? new Set();
      native.listeners.set(event, listeners); listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
});
vi.mock('@tauri-apps/api/webviewWindow', () => ({
  getCurrentWebviewWindow: () => ({
    onDragDropEvent: async (callback: typeof native.onDrop) => { native.onDrop = callback; return () => { native.onDrop = undefined; }; }, isVisible: async () => true,
    show: async () => {}, setFocus: async () => {}, isFocused: async () => true,
  }),
}));

beforeEach(() => {
  native.listeners.clear(); native.draft = null; native.images.clear(); native.binding = null; native.active = null;
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command, args) => {
    const values = args as Record<string, any> | undefined;
    if (command === 'get_build_instance') return { instance: 'capture-test' };
    if (command === 'get_client_token') return 'test-token';
    if (command === 'capture_hide') { native.onHide?.(); return; }
    if (command === 'capture_status') return { binding: native.binding, active: native.active };
    if (command === 'capture_bind') { native.active = values!.binding; return; }
    if (command === 'capture_cache') { native.binding = values!.binding; return; }
    if (command === 'capture_draft_read') {
      const draft = native.draft as CaptureDraft | null;
      for (const image of draft?.images ?? []) if (image.url) native.images.set(image.id, image.url);
      return draft && { ...draft, images: draft.images.map(image => ({ ...image, url: image.url || native.images.get(image.id)! })) };
    }
    if (command === 'capture_draft_write') { native.draft = values!.draft; return; }
    if (command === 'capture_draft_image_write') { native.images.set(values!.id, values!.url); return; }
    return undefined;
  });
});

async function captureApp(configure?: (daemon: ScriptedDaemon) => void, options: Parameters<typeof renderApp>[0] = {}) {
  const hidden = new Promise<void>(resolve => { native.onHide = resolve; });
  const view = await renderApp(options);
  serveSettings(view.daemon);
  configure?.(view.daemon);
  const capture = render(<QuickCapture />);
  await act(async () => {
    for (const listener of native.listeners.get(CAPTURE_READY) ?? []) await listener({ payload: undefined });
  });
  await view.daemon.idle();
  return { ...view, hidden, capture };
}
const editor = () => screen.getByRole('textbox', { name: 'Capture message' });
type WireRecord = NonNullable<NonNullable<EventMessage<'capture_result'>['result']>['record']>;
function record(command: { capture_id: string; content?: string; target?: WireRecord['target'] }): WireRecord {
  return { id: command.capture_id, content: command.content ?? 'saved note', target: command.target ?? { kind: 'chief' },
    attachments: [], created_at: '2026-10-01T12:00:00Z' };
}
function captureTraffic(daemon: ScriptedDaemon) { return daemon.sent.filter(command => command.cmd.startsWith('capture_')); }

// These enter through the real main App/socket and secondary-window bridge; native focus is a packaged scenario.
describe('Quick Capture app wire behavior', () => {
  it('shares the main app text size through the secondary-window bridge', async () => {
    const { daemon } = await captureApp();
    const resize = async (change: 'increase' | 'decrease' | 'reset') => {
      await act(async () => {
        for (const listener of native.listeners.get(CAPTURE_REQUEST) ?? []) {
          await listener({ payload: { id: crypto.randomUUID(), action: 'font', change } });
        }
      });
      await daemon.idle();
    };
    await resize('increase');
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'uiScale', value: '1.1' });
    expect(editor().closest('main')!.style.getPropertyValue('--ui-scale')).toBe('1.1');
    expect(document.documentElement.style.getPropertyValue('--ui-scale')).toBe('1.1');
    await resize('decrease');
    expect(editor().closest('main')!.style.getPropertyValue('--ui-scale')).toBe('1');
    await resize('increase');
    await resize('reset');
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'uiScale', value: '1' });
    expect(editor().closest('main')!.style.getPropertyValue('--ui-scale')).toBe('1');
    expect(editor()).toHaveValue('');
  });

  it('edits and disables its global binding through the main keyboard mapping', async () => {
    const { daemon } = await captureApp();
    await gesture(daemon, () => pressShortcut('ui.showShortcuts'));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Edit shortcuts' })));
    const mapping = within(screen.getByRole('region', { name: 'Global Quick Capture shortcut' }));
    await gesture(daemon, () => fireEvent.click(mapping.getByRole('button', { name: 'Change' })));
    await gesture(daemon, () => fireEvent.keyDown(mapping.getByRole('button', { name: 'Press shortcut…' }), { key: 'a', code: 'KeyA' }));
    await gesture(daemon, () => fireEvent.keyDown(mapping.getByRole('button', { name: 'Press shortcut…' }), { key: 'A', code: 'KeyA', shiftKey: true }));
    expect(mapping.getByRole('alert')).toHaveTextContent('Use Command, Control or Option');
    expect(daemon.sentOf('set_setting').filter(command => command.key === 'capture.shortcut')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(mapping.getByRole('button', { name: 'Press shortcut…' }), { key: 'b', code: 'KeyB', ctrlKey: true, altKey: true }));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'capture.shortcut', value: 'Control+Alt+KeyB' });
    expect(native.active).toBe('Control+Alt+KeyB');
    await gesture(daemon, () => fireEvent.click(mapping.getByRole('button', { name: 'Turn off' })));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'capture.shortcut', value: '' });
    expect(native.active).toBeNull();
    expect(mapping.getByText('Active: Off')).toBeInTheDocument();
  });

  it('sends once with stable identity and clears only after a durable receipt', async () => {
    const { daemon, hidden } = await captureApp();
    expect(screen.queryByRole('button', { name: 'Shortcut settings' })).not.toBeInTheDocument();
    expect(screen.queryByText('Open capture from anywhere')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Keep the launch note' } }));
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('capture_send');
    expect(request).toMatchObject({ target: { kind: 'chief' }, content: 'Keep the launch note', attachment_ids: [] });
    expect(editor()).toHaveValue('Keep the launch note');
    expect(screen.getByRole('button', { name: /Send|Retry/ })).toBeDisabled();
    expect(native.draft).toMatchObject({ id: request.capture_id, uncertain: true });
    await act(async () => { daemon.replyTo(request, { event: 'capture_result', request_id: request.request_id, success: true, result: { record: record(request) } }); await hidden; });
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(native.draft).toMatchObject({ text: '', recipient: 'chief', uncertain: false });
    expect(daemon.sentOf('capture_send')).toHaveLength(1);
    expect(captureTraffic(daemon).map(command => command.cmd)).toEqual(['capture_send']);
  });

  it('resolves a lost acknowledgment by id and never submits a duplicate', async () => {
    const { daemon } = await captureApp();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Uncertain note' } }));
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('capture_send');
    daemon.on('capture_get', command => ({ event: 'capture_result', success: true, result: { record: record({ ...request, capture_id: command.capture_id }) } }));
    await act(async () => daemon.disconnect(1009, 'WebSocket max_message_bytes=1048576, received at least 1048577'));
    await daemon.idle();
    expect(editor()).toHaveValue('Uncertain note');
    expect(screen.getByRole('alert')).toHaveTextContent('max_message_bytes=1048576, received at least 1048577');
    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('capture_get').map(command => command.capture_id)).toEqual([request.capture_id]);
    expect(editor()).toHaveValue('');
    expect(daemon.sentOf('capture_send')).toHaveLength(1);
  });

  it('uploads restored image bytes before sending and retains the image on an upload failure', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    const url = 'data:image/png;base64,iVBORw0KGgo=';
    native.draft = { id: captureId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'screenshot.png', url }] };
    const { daemon } = await captureApp(daemon => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).startsWith('data:')) return localFetch(input, init);
      throw new Error('app wire tests reach no network');
    });
    daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: [] } } }));
    daemon.on('capture_attachment_put', () => ({ event: 'capture_result', success: false, error: 'Upload storage unavailable' }));
    daemon.on('capture_get', () => ({ event: 'capture_result', success: false, error: 'Capture absent', error_code: 'capture_not_found' }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const upload = await daemon.received('capture_attachment_put');
    await daemon.idle();
    expect(upload).toMatchObject({ capture_id: captureId, attachment_id: imageId, name: 'screenshot.png',
      offset: 0, data_base64: 'iVBORw0KGgo=', final: true });
    expect(daemon.sentOf('capture_send')).toEqual([]);
    expect(screen.getByRole('img', { name: 'screenshot.png' })).toBeInTheDocument();
    expect(native.draft).toMatchObject({ id: captureId, images: [{ id: imageId }] });
  });

  it('reconciles a ready image after relaunch and sends an image-only capture without reuploading it', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon, hidden } = await captureApp(daemon => {
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('capture_send');
    expect(request).toMatchObject({ capture_id: captureId, content: '', attachment_ids: [imageId] });
    expect(daemon.sentOf('capture_attachment_put')).toEqual([]);
    await act(async () => { daemon.replyTo(request, { event: 'capture_result', request_id: request.request_id,
      success: true, result: { record: record(request) } }); await hidden; });
    expect(screen.queryByRole('img', { name: 'kept.png' })).toBeNull();
  });

  it('drains an eager image upload before deleting a removed attachment', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'remove.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true,
        result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('capture_attachment_discard', () => ({ event: 'capture_result', success: true, result: { discarded: true } }));
    });
    const upload = await daemon.received('capture_attachment_put');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove remove.png' })));
    expect(daemon.sentOf('capture_attachment_discard')).toEqual([]);
    await act(async () => { daemon.replyTo(upload, { event: 'capture_result', request_id: upload.request_id,
      success: true, result: { upload: { next_offset: 8 } } }); });
    const discarded = await daemon.received('capture_attachment_discard');
    await daemon.idle();
    expect(discarded).toMatchObject({ capture_id: captureId, attachment_id: imageId });
    expect(screen.queryByRole('img', { name: 'remove.png' })).toBeNull();
    expect(daemon.sentOf('capture_send')).toEqual([]);
    expect(native.draft).toMatchObject({ images: [] });
  });

  it('IME Enter keeps composing, while Shift+Enter does not send', async () => {
    const { daemon } = await captureApp();
    await gesture(daemon, () => {
      fireEvent.change(editor(), { target: { value: '日本語' } });
      fireEvent.compositionStart(editor()); fireEvent.keyDown(editor(), { key: 'Enter', isComposing: true, keyCode: 229 });
      fireEvent.compositionEnd(editor()); fireEvent.keyDown(editor(), { key: 'Enter', shiftKey: true });
    });
    expect(editor()).toHaveValue('日本語');
    expect(captureTraffic(daemon)).toEqual([]);
  });

  it.each(['paste', 'drop'] as const)('attaches a PDF by %s, stages its bytes and submits without image decoding', async mode => {
    const pdf = '%PDF-1.4\nQuick Capture PDF fixture\n%%EOF\n';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'capture_image_read'
      ? `data:application/pdf;base64,${btoa(pdf)}` : nativeInvoke(command, args));
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('capture_attachment_put', command => ({ event: 'capture_result', success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('capture_send', command => ({ event: 'capture_result', success: true, result: { record: record(command) } }));
    });
    await act(async () => {
      if (mode === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File([pdf], 'notes.pdf', { type: 'application/pdf' })] } });
      else await native.onDrop!({ payload: { type: 'drop', paths: ['/fixture/notes.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('capture_attachment_put');
    await daemon.idle();
    expect(atob(upload.data_base64)).toBe(pdf);
    expect(upload.name).toBe('notes.pdf');
    expect(screen.getByText('notes.pdf')).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'notes.pdf' })).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(daemon.sentOf('capture_send')).toMatchObject([{ capture_id: upload.capture_id, attachment_ids: [upload.attachment_id] }]);
  });

  it('shares the measured work budget across additions and uploads, skips removals and releases failed work', async () => {
    const reads: string[] = [];
    let releaseFirst!: (value: string) => void;
    const first = new Promise<string>(resolve => { releaseFirst = resolve; });
    const url = 'data:application/pdf;base64,JVBERi0xLjQK';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command !== 'capture_image_read') return nativeInvoke(command, args);
      const path = (args as { path: string }).path;
      reads.push(path);
      return path === '/first.pdf' ? first : url;
    });
    let uploadsHeld = true;
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('capture_attachment_put', command => uploadsHeld ? undefined : ({ event: 'capture_result', success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('capture_send', command => ({ event: 'capture_result', success: true, result: { record: record(command) } }));
      daemon.on('capture_attachment_discard', () => ({ event: 'capture_result', success: true, result: { discarded: true } }));
    });
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { batchSize: 1 },
      } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf', '/removed.pdf'], position: { x: 100, y: 100 } } });
    });
    expect(reads).toEqual(['/first.pdf']);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove removed.pdf' })));
    await act(async () => { releaseFirst(url); });
    const upload = await daemon.received('capture_attachment_put');
    await act(async () => { await native.onDrop!({ payload: { type: 'drop', paths: ['/later.pdf'], position: { x: 100, y: 100 } } }); });
    await daemon.idle();
    expect(reads).toEqual(['/first.pdf']);
    uploadsHeld = false;
    await act(async () => { daemon.replyTo(upload, { event: 'capture_result', request_id: upload.request_id, success: false, error: 'Upload interrupted' }); });
    await daemon.received('capture_attachment_put', command => command.name === 'later.pdf');
    expect(reads).toEqual(['/first.pdf', '/later.pdf']);
    await daemon.idle();
    expect(screen.getByText('first.pdf')).toBeInTheDocument();
    expect(screen.getByText('later.pdf')).toBeInTheDocument();
    uploadsHeld = false;
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(daemon.sentOf('capture_send')[0].attachment_ids).toHaveLength(2);
    expect(daemon.sentOf('capture_attachment_put').filter(command => command.name === 'first.pdf')).toHaveLength(2);
    expect(daemon.sentOf('capture_attachment_put').filter(command => command.name === 'removed.pdf')).toHaveLength(0);
  });

  it('retains edits and removal made while another file occupies the upload queue', async () => {
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'capture_image_read'
      ? 'data:application/pdf;base64,JVBERi0xLjQK' : nativeInvoke(command, args));
    let uploadsHeld = true;
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('capture_attachment_put', command => uploadsHeld ? undefined : ({ event: 'capture_result', success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('capture_attachment_discard', () => ({ event: 'capture_result', success: true, result: { discarded: true } }));
    });
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { batchSize: 1 },
      } });
      fireEvent.change(editor(), { target: { value: 'Before upload' } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf', '/second.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('capture_attachment_put', command => command.name === 'first.pdf');
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Edited while uploading' } }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove first.pdf' })));
    uploadsHeld = false;
    await act(async () => { daemon.replyTo(upload, { event: 'capture_result', request_id: upload.request_id, success: true,
      result: { upload: { next_offset: atob(upload.data_base64).length } } }); });
    await daemon.received('capture_attachment_put', command => command.name === 'second.pdf');
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    const retained = native.draft as CaptureDraft;
    expect(retained.text).toBe('Edited while uploading');
    expect(retained.images.map(file => file.name)).toEqual(['second.pdf']);
  });

  it('retains the draft and resumes file staging after a disconnect drains the upload queue', async () => {
    const reads: string[] = [];
    const url = 'data:application/pdf;base64,JVBERi0xLjQK';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command !== 'capture_image_read') return nativeInvoke(command, args);
      reads.push((args as { path: string }).path); return url;
    });
    let held = true;
    const { daemon } = await captureApp(daemon => {
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('capture_attachment_put', command => held ? undefined : ({ event: 'capture_result', success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
    });
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { batchSize: 1 },
      } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf'], position: { x: 100, y: 100 } } });
    });
    await daemon.received('capture_attachment_put');
    await act(async () => {
      await native.onDrop!({ payload: { type: 'drop', paths: ['/second.pdf'], position: { x: 100, y: 100 } } });
      daemon.disconnect();
    });
    await daemon.idle();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(editor()).toBeInTheDocument();
    expect(reads).toEqual(['/first.pdf', '/second.pdf']);
    expect((native.draft as CaptureDraft).images.map(file => file.name)).toEqual(['first.pdf', 'second.pdf']);
    held = false;
    await daemon.reconnect();
    await daemon.received('capture_attachment_put', command => command.name === 'second.pdf');
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    expect(daemon.sentOf('capture_attachment_put').filter(command => command.name === 'first.pdf')).toHaveLength(2);
    expect(daemon.sentOf('capture_attachment_put').filter(command => command.name === 'second.pdf')).toHaveLength(1);
  });

  it.each([{ length: 0, offset: 0 }, { length: 524291, offset: 0 }, { length: 524292, offset: 1 }, { length: 524293, offset: 2 }])(
    'uploads exact retained bytes (length=$length, resumed offset=$offset) without a whole-file URL fetch', async ({ length, offset }) => {
      const captureId = crypto.randomUUID(), attachmentId = crypto.randomUUID();
      const bytes = Uint8Array.from({ length }, (_, index) => index % 256);
      const binary = Array.from(bytes, byte => String.fromCharCode(byte)).join('');
      native.draft = { id: captureId, text: 'Binary attachment', recipient: 'chief', uncertain: false,
        images: [{ id: attachmentId, name: 'retained.bin', url: `data:application/octet-stream;base64,${btoa(binary)}` }] };
      const { daemon } = await captureApp(daemon => {
        vi.stubGlobal('fetch', () => { throw new Error('retained files upload directly from their base64 bytes'); });
        daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [],
          draft_assets: offset ? [{ capture_id: captureId, attachment_id: attachmentId, name: 'retained.bin', state: 'staged', next_offset: offset }] : [] } } }));
        daemon.on('capture_attachment_put', command => ({ event: 'capture_result', success: true,
          result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
        daemon.on('capture_send', command => ({ event: 'capture_result', success: true, result: { record: record(command) } }));
      });
      await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
      const uploads = daemon.sentOf('capture_attachment_put');
      expect(uploads[0].offset).toBe(offset);
      expect(uploads.map(command => atob(command.data_base64)).join('')).toBe(binary.slice(offset));
      expect(uploads[uploads.length - 1].final).toBe(true);
      expect(daemon.sentOf('capture_send')[0].attachment_ids).toEqual([attachmentId]);
    });

  it('Recent shows sent/read history and files without delivery actions or inbox reads', async () => {
    const { daemon } = await captureApp(daemon => {
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [
        record({ capture_id: crypto.randomUUID(), content: 'Sent note' }),
        { ...record({ capture_id: crypto.randomUUID(), content: 'Read note' }), read_at: '2026-10-01T12:01:00Z',
          attachments: [{ id: crypto.randomUUID(), name: 'notes.pdf', media_type: 'application/pdf', bytes: 51 }] },
      ], draft_assets: [] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent captures' })));
    const recent = screen.getByRole('region', { name: 'Recent captures' });
    expect(within(recent).getByText('Sent note')).toBeInTheDocument();
    expect(within(recent).getByText('Read note')).toBeInTheDocument();
    expect(within(recent).getByText('notes.pdf')).toBeInTheDocument();
    expect(within(recent).getByText(/^Sent ·/)).toBeInTheDocument();
    expect(within(recent).getByText(/^Read ·/)).toBeInTheDocument();
    expect(within(recent).queryByRole('button', { name: /Cancel|Restore|Retry delivery|Open recipient|Follow up/ })).toBeNull();
    expect(within(recent).queryByRole('combobox')).toBeNull();
    expect(daemon.sentOf('agent_inbox')).toEqual([]);
    expect(captureTraffic(daemon).map(command => command.cmd)).toEqual(['capture_list']);
  });

  it('resends retained image bytes after a failed draft discard', async () => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID(), secondId = crypto.randomUUID();
    native.draft = { id: captureId, text: 'Keep this note', recipient: 'chief', uncertain: false,
      images: [imageId, secondId].map(id => ({ id, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' })) };
    let discarded = false;
    const { daemon, capture } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [], draft_assets: discarded ? [] : [imageId, secondId].map(id => ({ capture_id: captureId, attachment_id: id, name: 'kept.png', state: 'ready', next_offset: 8 })) } } }));
      daemon.on('capture_attachment_discard', command => {
        expect(native.draft).toMatchObject({ text: 'Keep this note' });
        expect((native.draft as { id: string }).id).not.toBe(captureId);
        discarded = true; return command.attachment_id === imageId
        ? { event: 'capture_result', success: true, result: { discarded: true } }
        : { event: 'capture_result', success: false, error: 'Discard acknowledgment lost' }; });
      daemon.on('capture_attachment_put', command => command.capture_id === captureId
        ? { event: 'capture_result', success: false, error: 'Attachment was discarded; upload with a new identity' }
        : { event: 'capture_result', success: true, result: { upload: { next_offset: 8 } } });
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent captures' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Discard acknowledgment lost');
    capture.unmount();
    render(<QuickCapture />);
    await act(async () => {
      for (const listener of native.listeners.get(CAPTURE_READY) ?? []) await listener({ payload: undefined });
    });
    await daemon.idle();
    expect(editor()).toHaveValue('Keep this note');
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const sent = await daemon.received('capture_send');
    expect(sent).toMatchObject({ content: 'Keep this note', attachment_ids: [imageId, secondId] });
    expect(sent.capture_id).not.toBe(captureId);
    expect(daemon.sentOf('capture_attachment_put')).toMatchObject([imageId, secondId].map(id => ({ capture_id: sent.capture_id, attachment_id: id, data_base64: 'iVBORw0KGgo=' })));
    expect(native.draft).toMatchObject({ id: expect.any(String) });
  });

  it('keeps the draft without remote deletion when its replacement identity cannot be saved', async () => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: 'Keep this note', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'capture_draft_write' && (args as { draft: CaptureDraft }).draft.id !== captureId) {
        throw new Error('Local draft storage unavailable');
      }
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent captures' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Local draft storage unavailable');
    expect(daemon.sentOf('capture_attachment_discard')).toEqual([]);
    expect(native.draft).toMatchObject({ id: captureId, text: 'Keep this note' });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Back to note/ })));
    expect(editor()).toHaveValue('Keep this note');
    expect(screen.getByRole('img', { name: 'kept.png' })).toBeInTheDocument();
  });

  it.each(['paste', 'drop'])('blocks %s images until the retained draft is restored', async source => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID();
    const stored: CaptureDraft = { id: captureId, text: 'Retained note', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'saved.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    let finishRead!: (draft: CaptureDraft) => void;
    const read = new Promise<CaptureDraft>(resolve => { finishRead = resolve; });
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'capture_draft_read') return read;
      if (command === 'capture_image_read') throw new Error('Synthetic drop read failure');
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('capture_list', () => ({ event: 'capture_result', success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'saved.png', state: 'ready', next_offset: 8 }] } } }));
    });
    expect(editor()).toHaveAttribute('readonly');
    await gesture(daemon, () => {
      if (source === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File(['image'], 'new.png', { type: 'image/png' })] } });
      else return native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } });
    });
    expect(screen.getByRole('alert')).toHaveTextContent('Draft is still loading');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'capture_image_read')).toHaveLength(0);
    expect(daemon.sentOf('capture_attachment_put')).toEqual([]);
    await act(async () => finishRead(stored)); await daemon.idle();
    expect(editor()).toHaveValue('Retained note'); expect(editor()).not.toHaveAttribute('readonly');
    expect(screen.getByRole('img', { name: 'saved.png' })).toBeInTheDocument();
    if (source === 'drop') {
      await gesture(daemon, () => native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } }));
      expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'capture_image_read')).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent('Synthetic drop read failure');
    }
  });

});
