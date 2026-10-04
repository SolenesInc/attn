import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { QuickCapture } from './components/QuickCapture';
import { QUICK_CAPTURE_READY, QUICK_CAPTURE_REQUEST } from './quickCapture/client';
import { DEFAULT_PROFILE_ID, defaultProfile, emptyDesktop } from './test/daemonFixtures';
import type { CachedUserMessageDraft } from './quickCapture/draft';
import { QuickCaptureAutomationWorkQueue } from './quickCapture/workQueueAutomation';
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
  drafts: new Map<string, unknown>(),
  images: new Map<string, string>(),
  visible: false,
  binding: null as string | null,
  active: null as string | null,
  onHide: null as (() => void) | null,
  onDrop: undefined as ((event: { payload: { type: string; paths: string[]; position: { x: number; y: number } } }) => unknown) | undefined,
}); });
vi.mock('@tauri-apps/api/event', () => {
  async function emitTo(_label: string, event: string, payload?: unknown) {
    if (event === 'capture-open') native.visible = true;
    if (event === 'capture-hidden') native.visible = false;
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
    onDragDropEvent: async (callback: typeof native.onDrop) => { native.onDrop = callback; return () => { native.onDrop = undefined; }; }, isVisible: async () => native.visible,
    show: async () => {}, setFocus: async () => {}, isFocused: async () => true,
  }),
}));

beforeEach(() => {
  native.listeners.clear(); native.visible = false; native.draft = null; native.drafts.clear(); native.images.clear(); native.binding = null; native.active = null;
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command, args) => {
    const values = args as Record<string, any> | undefined;
    if (command === 'get_build_instance') return { instance: 'capture-test' };
    if (command === 'get_client_token') return 'test-token';
    if (command === 'capture_hide') { native.onHide?.(); return; }
    if (command === 'capture_status') return { binding: native.binding, active: native.active };
    if (command === 'capture_bind') { native.active = values!.binding; return; }
    if (command === 'capture_cache') { native.binding = values!.binding; return; }
    if (command === 'user_message_draft_read') {
      const draft = (native.drafts.get(values!.profileId) ?? (native.drafts.size === 0 ? native.draft : null)) as CachedUserMessageDraft | null;
      for (const image of draft?.images ?? []) if (image.url) native.images.set(`${values!.profileId}:${image.id}`, image.url);
      return draft && { ...draft, images: draft.images.map(image => ({ ...image, url: image.url || native.images.get(`${values!.profileId}:${image.id}`)! })) };
    }
    if (command === 'user_message_draft_write') { native.draft = values!.draft; native.drafts.set(values!.profileId, values!.draft); return; }
    if (command === 'user_message_draft_image_write') { native.images.set(`${values!.profileId}:${values!.id}`, values!.url); return; }
    return undefined;
  });
});

async function captureApp(configure?: (daemon: ScriptedDaemon) => void, options: Parameters<typeof renderApp>[0] = {}) {
  const hidden = new Promise<void>(resolve => { native.onHide = resolve; });
  const view = await renderApp(options);
  serveSettings(view.daemon);
  configure?.(view.daemon);
  const capture = render(<QuickCapture workQueue={new QuickCaptureAutomationWorkQueue()} />);
  await act(async () => {
    for (const listener of native.listeners.get(QUICK_CAPTURE_READY) ?? []) await listener({ payload: undefined });
  });
  await view.daemon.idle();
  return { ...view, hidden, capture };
}
const editor = () => screen.getByRole('textbox', { name: 'User message' });
type WireRecord = NonNullable<NonNullable<EventMessage<'user_message_result'>['result']>['record']>;
function record(command: { message_id: string; content?: string; target?: WireRecord['target'] }): WireRecord {
  return { id: command.message_id, content: command.content ?? 'saved note', target: command.target ?? { kind: 'chief' },
    attachments: [], created_at: '2026-10-01T12:00:00Z' };
}
function userMessageTraffic(daemon: ScriptedDaemon) { return daemon.sent.filter(command => command.cmd.startsWith('user_message_')); }

describe('Quick Capture app wire behavior', () => {
  it('keeps edits made after returning to a profile when an older resolve finishes', async () => {
    const a = defaultProfile('desktop-1');
    const b = defaultProfile('desktop-work', { id: 'profile-work', name: 'Work' });
    const desktops = [emptyDesktop('desktop-1'), emptyDesktop('desktop-work', { profile_id: b.id })];
    native.draft = { id: crypto.randomUUID(), text: 'Retained A', recipient: 'chief', images: [], uncertain: true };
    let resolutions = 0;
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_get', command => {
        resolutions++;
        if (resolutions === 2) return;
        return { event: 'user_message_result', profile_id: command.profile_id!, success: false,
          error: resolutions === 1 ? 'Storage is temporarily unavailable' : 'User message absent',
          ...(resolutions === 1 ? {} : { error_code: 'user_message_not_found' }) };
      });
    }, { initialState: { profiles: [a, b], desktops, selected_profile_id: a.id } });
    await daemon.idle();
    expect(resolutions).toBe(1);
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(resolutions).toBe(2);
    const held = daemon.sentOf('user_message_get')[1];
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: a, desktops: [desktops[0]] }));
    await daemon.idle();
    expect(resolutions).toBe(3);
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Edited after returning to A' } }));
    await act(async () => daemon.replyTo(held, { event: 'user_message_result', profile_id: a.id, request_id: held.request_id, success: false, error: 'User message absent', error_code: 'user_message_not_found' }));
    await daemon.idle();
    expect(editor()).toHaveValue('Edited after returning to A');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'Edited after returning to A', uncertain: false });
    expect(daemon.sentOf('user_message_send')).toEqual([]);
  });

  it('reconciles a late acceptance in its owning profile without hiding another profile', async () => {
    const a = defaultProfile('desktop-1');
    const b = defaultProfile('desktop-work', { id: 'profile-work', name: 'Work' });
    const desktops = [emptyDesktop('desktop-1'), emptyDesktop('desktop-work', { profile_id: b.id })];
    const { daemon } = await captureApp(undefined, { initialState: { profiles: [a, b], desktops, selected_profile_id: a.id } });
    let hides = 0;
    native.onHide = () => { hides++; };
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'A request' } }));
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const submission = await daemon.received('user_message_send');
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'B draft' } }));
    await act(async () => daemon.replyTo(submission, { event: 'user_message_result', profile_id: a.id, request_id: submission.request_id, success: true, result: { record: record(submission) } }));
    await daemon.idle();
    expect(hides).toBe(0);
    expect(editor()).toHaveValue('B draft');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'A request', uncertain: true });
    daemon.on('user_message_get', command => ({ event: 'user_message_result', profile_id: command.profile_id!, success: true, result: { record: record(submission) } }));
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: a, desktops: [desktops[0]] }));
    const reconciliation = await daemon.received('user_message_get');
    expect(reconciliation).toMatchObject({ profile_id: a.id, message_id: submission.message_id });
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(native.drafts.get(b.id)).toMatchObject({ text: 'B draft' });
    expect(hides).toBe(0);
  });

  it('retains separate profile drafts and keeps a resumed upload in its starting profile', async () => {
    const other = 'profile-work';
    const a = defaultProfile('desktop-1');
    const b = defaultProfile('desktop-work', { id: other, name: 'Work' });
    const desktops = [emptyDesktop('desktop-1'), emptyDesktop('desktop-work', { profile_id: other })];
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    const url = `data:application/pdf;base64,${btoa('A'.repeat(524288 + 1))}`;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'user_message_image_read' ? url : nativeInvoke(command, args));
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_list', command => ({ event: 'user_message_result', profile_id: command.profile_id!, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_put', command => command.offset === 0 ? undefined : ({ event: 'user_message_result', profile_id: other, success: false, error: 'The selected profile changed' }));
    }, { initialState: { profiles: [a, b], desktops, selected_profile_id: a.id } });
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'A retained draft' } }));
    await act(async () => { await native.onDrop!({ payload: { type: 'drop', paths: ['/a.pdf'], position: { x: 100, y: 100 } } }); });
    const first = await daemon.received('user_message_attachment_put');
    expect(first).toMatchObject({ profile_id: a.id, offset: 0, final: false });
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(screen.queryByRole('img', { name: 'a.pdf' })).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'B retained draft' } }));
    await act(async () => daemon.replyTo(first, { event: 'user_message_result', profile_id: a.id, request_id: first.request_id, success: true, result: { upload: { next_offset: 524288 } } }));
    await daemon.idle();
    expect(daemon.sentOf('user_message_attachment_put')).toHaveLength(2);
    expect(daemon.sentOf('user_message_attachment_put')[1]).toMatchObject({ profile_id: a.id, offset: 524288, final: true });
    expect(daemon.sentOf('user_message_send')).toEqual([]);
    expect(editor()).toHaveValue('B retained draft');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'A retained draft', images: [{ name: 'a.pdf' }] });
    expect(native.drafts.get(b.id)).toMatchObject({ text: 'B retained draft', images: [] });
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: a, desktops: [desktops[0]] }));
    await daemon.idle();
    expect(editor()).toHaveValue('A retained draft');
    expect(screen.getByText('a.pdf')).toBeInTheDocument();
  });

  it('shares the main app text size through the secondary-window bridge', async () => {
    const { daemon } = await captureApp();
    const resize = async (change: 'increase' | 'decrease' | 'reset') => {
      await act(async () => {
        for (const listener of native.listeners.get(QUICK_CAPTURE_REQUEST) ?? []) {
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
    const request = await daemon.received('user_message_send');
    expect(request).toMatchObject({ target: { kind: 'chief' }, content: 'Keep the launch note', attachment_ids: [] });
    expect(editor()).toHaveValue('Keep the launch note');
    expect(screen.getByRole('button', { name: /Send|Retry/ })).toBeDisabled();
    expect(native.draft).toMatchObject({ id: request.message_id, uncertain: true });
    await act(async () => { daemon.replyTo(request, { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, request_id: request.request_id, success: true, result: { record: record(request) } }); await hidden; });
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(native.draft).toMatchObject({ text: '', recipient: 'chief', uncertain: false });
    expect(daemon.sentOf('user_message_send')).toHaveLength(1);
    expect(userMessageTraffic(daemon).map(command => command.cmd)).toEqual(['user_message_send']);
    expect(screen.getByText('Saved for Chief')).toBeInTheDocument();
    daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: {
      items: [record(request)], draft_assets: [],
    } } }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    expect(within(screen.getByRole('region', { name: 'Recent messages' })).getByText('Keep the launch note')).toBeInTheDocument();
    expect(screen.queryByText('Saved for Chief')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Back to draft/ })));
    expect(screen.getByText('Saved for Chief')).toBeInTheDocument();
  });

  it('resolves a lost acknowledgment by id and never submits a duplicate', async () => {
    const { daemon } = await captureApp();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Uncertain note' } }));
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('user_message_send');
    daemon.on('user_message_get', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record({ ...request, message_id: command.message_id }) } }));
    await act(async () => daemon.disconnect(1009, 'WebSocket max_message_bytes=1048576, received at least 1048577'));
    await daemon.idle();
    expect(editor()).toHaveValue('Uncertain note');
    expect(screen.getByRole('alert')).toHaveTextContent('max_message_bytes=1048576, received at least 1048577');
    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('user_message_get').map(command => command.message_id)).toEqual([request.message_id]);
    expect(editor()).toHaveValue('');
    expect(daemon.sentOf('user_message_send')).toHaveLength(1);
  });

  it('uploads restored image bytes before sending and retains the image on an upload failure', async () => {
    const messageId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    const url = 'data:image/png;base64,iVBORw0KGgo=';
    native.draft = { id: messageId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'screenshot.png', url }] };
    const { daemon } = await captureApp(daemon => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).startsWith('data:')) return localFetch(input, init);
      throw new Error('app wire tests reach no network');
    });
    daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
    daemon.on('user_message_attachment_put', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Upload storage unavailable' }));
    daemon.on('user_message_get', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'User message absent', error_code: 'user_message_not_found' }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const upload = await daemon.received('user_message_attachment_put');
    await daemon.idle();
    expect(upload).toMatchObject({ message_id: messageId, attachment_id: imageId, name: 'screenshot.png',
      offset: 0, data_base64: 'iVBORw0KGgo=', final: true });
    expect(daemon.sentOf('user_message_send')).toEqual([]);
    expect(screen.getByRole('img', { name: 'screenshot.png' })).toBeInTheDocument();
    expect(native.draft).toMatchObject({ id: messageId, images: [{ id: imageId }] });
  });

  it('reconciles a ready image after relaunch and sends an image-only capture without reuploading it', async () => {
    const messageId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: messageId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon, hidden } = await captureApp(daemon => {
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ message_id: messageId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('user_message_send');
    expect(request).toMatchObject({ message_id: messageId, content: '', attachment_ids: [imageId] });
    expect(daemon.sentOf('user_message_attachment_put')).toEqual([]);
    await act(async () => { daemon.replyTo(request, { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, request_id: request.request_id,
      success: true, result: { record: record(request) } }); await hidden; });
    expect(screen.queryByRole('img', { name: 'kept.png' })).toBeNull();
  });

  it('drains an eager image upload before deleting a removed attachment', async () => {
    const messageId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: messageId, text: '', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'remove.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_discard', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }));
    });
    const upload = await daemon.received('user_message_attachment_put');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove remove.png' })));
    expect(daemon.sentOf('user_message_attachment_discard')).toEqual([]);
    await act(async () => { daemon.replyTo(upload, { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, request_id: upload.request_id,
      success: true, result: { upload: { next_offset: 8 } } }); });
    const discarded = await daemon.received('user_message_attachment_discard');
    await daemon.idle();
    expect(discarded).toMatchObject({ message_id: messageId, attachment_id: imageId });
    expect(screen.queryByRole('img', { name: 'remove.png' })).toBeNull();
    expect(daemon.sentOf('user_message_send')).toEqual([]);
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
    expect(userMessageTraffic(daemon)).toEqual([]);
  });

  it.each(['paste', 'drop'] as const)('attaches a PDF by %s, stages its bytes and submits without image decoding', async mode => {
    const pdf = '%PDF-1.4\nQuick Capture PDF fixture\n%%EOF\n';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'user_message_image_read'
      ? `data:application/pdf;base64,${btoa(pdf)}` : nativeInvoke(command, args));
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_put', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('user_message_send', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record(command) } }));
    });
    await act(async () => {
      if (mode === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File([pdf], 'notes.pdf', { type: 'application/pdf' })] } });
      else await native.onDrop!({ payload: { type: 'drop', paths: ['/fixture/notes.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('user_message_attachment_put');
    await daemon.idle();
    expect(atob(upload.data_base64)).toBe(pdf);
    expect(upload.name).toBe('notes.pdf');
    expect(screen.getByText('notes.pdf')).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'notes.pdf' })).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(daemon.sentOf('user_message_send')).toMatchObject([{ message_id: upload.message_id, attachment_ids: [upload.attachment_id] }]);
  });

  it('shares the measured work budget across additions and uploads, skips removals and releases failed work', async () => {
    const reads: string[] = [];
    let releaseFirst!: (value: string) => void;
    const first = new Promise<string>(resolve => { releaseFirst = resolve; });
    const url = 'data:application/pdf;base64,JVBERi0xLjQK';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command !== 'user_message_image_read') return nativeInvoke(command, args);
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
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_put', command => uploadsHeld ? undefined : ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('user_message_send', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record(command) } }));
      daemon.on('user_message_attachment_discard', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }));
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
    const upload = await daemon.received('user_message_attachment_put');
    await act(async () => { await native.onDrop!({ payload: { type: 'drop', paths: ['/later.pdf'], position: { x: 100, y: 100 } } }); });
    await daemon.idle();
    expect(reads).toEqual(['/first.pdf']);
    uploadsHeld = false;
    await act(async () => { daemon.replyTo(upload, { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, request_id: upload.request_id, success: false, error: 'Upload interrupted' }); });
    await daemon.received('user_message_attachment_put', command => command.name === 'later.pdf');
    expect(reads).toEqual(['/first.pdf', '/later.pdf']);
    await daemon.idle();
    expect(screen.getByText('first.pdf')).toBeInTheDocument();
    expect(screen.getByText('later.pdf')).toBeInTheDocument();
    uploadsHeld = false;
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(daemon.sentOf('user_message_send')[0].attachment_ids).toHaveLength(2);
    expect(daemon.sentOf('user_message_attachment_put').filter(command => command.name === 'first.pdf')).toHaveLength(2);
    expect(daemon.sentOf('user_message_attachment_put').filter(command => command.name === 'removed.pdf')).toHaveLength(0);
  });

  it('retains edits and removal made while another file occupies the upload queue', async () => {
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'user_message_image_read'
      ? 'data:application/pdf;base64,JVBERi0xLjQK' : nativeInvoke(command, args));
    let uploadsHeld = true;
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_put', command => uploadsHeld ? undefined : ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('user_message_attachment_discard', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }));
    });
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { batchSize: 1 },
      } });
      fireEvent.change(editor(), { target: { value: 'Before upload' } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf', '/second.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('user_message_attachment_put', command => command.name === 'first.pdf');
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Edited while uploading' } }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove first.pdf' })));
    uploadsHeld = false;
    await act(async () => { daemon.replyTo(upload, { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, request_id: upload.request_id, success: true,
      result: { upload: { next_offset: atob(upload.data_base64).length } } }); });
    await daemon.received('user_message_attachment_put', command => command.name === 'second.pdf');
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    const retained = native.draft as CachedUserMessageDraft;
    expect(retained.text).toBe('Edited while uploading');
    expect(retained.images.map(file => file.name)).toEqual(['second.pdf']);
  });

  it('retains the draft and resumes file staging after a disconnect drains the upload queue', async () => {
    const reads: string[] = [];
    const url = 'data:application/pdf;base64,JVBERi0xLjQK';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command !== 'user_message_image_read') return nativeInvoke(command, args);
      reads.push((args as { path: string }).path); return url;
    });
    let held = true;
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('user_message_attachment_put', command => held ? undefined : ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
    });
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { batchSize: 1 },
      } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf'], position: { x: 100, y: 100 } } });
    });
    await daemon.received('user_message_attachment_put');
    await act(async () => {
      await native.onDrop!({ payload: { type: 'drop', paths: ['/second.pdf'], position: { x: 100, y: 100 } } });
      daemon.disconnect();
    });
    await daemon.idle();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(editor()).toBeInTheDocument();
    expect(reads).toEqual(['/first.pdf', '/second.pdf']);
    expect((native.draft as CachedUserMessageDraft).images.map(file => file.name)).toEqual(['first.pdf', 'second.pdf']);
    held = false;
    await daemon.reconnect();
    await daemon.received('user_message_attachment_put', command => command.name === 'second.pdf');
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    expect(daemon.sentOf('user_message_attachment_put').filter(command => command.name === 'first.pdf')).toHaveLength(2);
    expect(daemon.sentOf('user_message_attachment_put').filter(command => command.name === 'second.pdf')).toHaveLength(1);
  });

  it.each([{ length: 0, offset: 0 }, { length: 524291, offset: 0 }, { length: 524292, offset: 1 }, { length: 524293, offset: 2 }])(
    'uploads exact retained bytes (length=$length, resumed offset=$offset) without a whole-file URL fetch', async ({ length, offset }) => {
      const messageId = crypto.randomUUID(), attachmentId = crypto.randomUUID();
      const bytes = Uint8Array.from({ length }, (_, index) => index % 256);
      const binary = Array.from(bytes, byte => String.fromCharCode(byte)).join('');
      native.draft = { id: messageId, text: 'Binary attachment', recipient: 'chief', uncertain: false,
        images: [{ id: attachmentId, name: 'retained.bin', url: `data:application/octet-stream;base64,${btoa(binary)}` }] };
      const { daemon } = await captureApp(daemon => {
        vi.stubGlobal('fetch', () => { throw new Error('retained files upload directly from their base64 bytes'); });
        daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
          draft_assets: offset ? [{ message_id: messageId, attachment_id: attachmentId, name: 'retained.bin', state: 'staged', next_offset: offset }] : [] } } }));
        daemon.on('user_message_attachment_put', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true,
          result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
        daemon.on('user_message_send', command => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record(command) } }));
      });
      await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
      const uploads = daemon.sentOf('user_message_attachment_put');
      expect(uploads[0].offset).toBe(offset);
      expect(uploads.map(command => atob(command.data_base64)).join('')).toBe(binary.slice(offset));
      expect(uploads[uploads.length - 1].final).toBe(true);
      expect(daemon.sentOf('user_message_send')[0].attachment_ids).toEqual([attachmentId]);
    });

  it('Recent shows sent/read history and files without delivery actions or inbox reads', async () => {
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [
        record({ message_id: crypto.randomUUID(), content: 'Sent note' }),
        { ...record({ message_id: crypto.randomUUID(), content: 'Read note' }), read_at: '2026-10-01T12:01:00Z',
          attachments: [{ id: crypto.randomUUID(), name: 'notes.pdf', media_type: 'application/pdf', bytes: 51 }] },
      ], draft_assets: [] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    const recent = screen.getByRole('region', { name: 'Recent messages' });
    expect(within(recent).getByText('Sent note')).toBeInTheDocument();
    expect(within(recent).getByText('Read note')).toBeInTheDocument();
    expect(within(recent).getByText('notes.pdf')).toBeInTheDocument();
    expect(within(recent).getByText('PDF', { exact: true })).toBeInTheDocument();
    expect(within(recent).getByText('51 B')).toBeInTheDocument();
    expect(within(recent).getByText(/^Sent /)).toBeInTheDocument();
    expect(within(recent).getByText(/^Read /)).toBeInTheDocument();
    expect(within(recent).queryByRole('button', { name: /Cancel|Restore|Retry delivery|Open recipient|Follow up/ })).toBeNull();
    expect(within(recent).queryByRole('combobox')).toBeNull();
    expect(daemon.sentOf('agent_inbox')).toEqual([]);
    expect(userMessageTraffic(daemon).map(command => command.cmd)).toEqual(['user_message_list']);
    await act(async () => daemon.emit({ event: 'user_message_changed', profile_id: DEFAULT_PROFILE_ID, message_id: crypto.randomUUID() }));
    await daemon.idle();
    expect(daemon.sentOf('user_message_list')).toHaveLength(2);
    expect(within(recent).getByText('Read note')).toBeInTheDocument();
  });

  it('resends retained image bytes after a failed draft discard', async () => {
    const messageId = crypto.randomUUID(), imageId = crypto.randomUUID(), secondId = crypto.randomUUID();
    native.draft = { id: messageId, text: 'Keep this note', recipient: 'chief', uncertain: false,
      images: [imageId, secondId].map(id => ({ id, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' })) };
    let discarded = false;
    const { daemon, capture } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: discarded ? [] : [imageId, secondId].map(id => ({ message_id: messageId, attachment_id: id, name: 'kept.png', state: 'ready', next_offset: 8 })) } } }));
      daemon.on('user_message_attachment_discard', command => {
        expect(native.draft).toMatchObject({ text: 'Keep this note' });
        expect((native.draft as { id: string }).id).not.toBe(messageId);
        discarded = true; return command.attachment_id === imageId
        ? { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }
        : { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Discard acknowledgment lost' }; });
      daemon.on('user_message_attachment_put', command => command.message_id === messageId
        ? { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Attachment was discarded; upload with a new identity' }
        : { event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { upload: { next_offset: 8 } } });
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Discard acknowledgment lost');
    capture.unmount();
    render(<QuickCapture workQueue={new QuickCaptureAutomationWorkQueue()} />);
    await act(async () => {
      for (const listener of native.listeners.get(QUICK_CAPTURE_READY) ?? []) await listener({ payload: undefined });
    });
    await daemon.idle();
    expect(editor()).toHaveValue('Keep this note');
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const sent = await daemon.received('user_message_send');
    expect(sent).toMatchObject({ content: 'Keep this note', attachment_ids: [imageId, secondId] });
    expect(sent.message_id).not.toBe(messageId);
    expect(daemon.sentOf('user_message_attachment_put')).toMatchObject([imageId, secondId].map(id => ({ message_id: sent.message_id, attachment_id: id, data_base64: 'iVBORw0KGgo=' })));
    expect(native.draft).toMatchObject({ id: expect.any(String) });
  });

  it('keeps the draft without remote deletion when its replacement identity cannot be saved', async () => {
    const messageId = crypto.randomUUID(), imageId = crypto.randomUUID();
    native.draft = { id: messageId, text: 'Keep this note', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'user_message_draft_write' && (args as { draft: CachedUserMessageDraft }).draft.id !== messageId) {
        throw new Error('Local draft storage unavailable');
      }
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ message_id: messageId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Local draft storage unavailable');
    expect(daemon.sentOf('user_message_attachment_discard')).toEqual([]);
    expect(native.draft).toMatchObject({ id: messageId, text: 'Keep this note' });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Back to draft/ })));
    expect(editor()).toHaveValue('Keep this note');
    expect(screen.getByRole('img', { name: 'kept.png' })).toBeInTheDocument();
  });

  it.each(['paste', 'drop'])('blocks %s images until the retained draft is restored', async source => {
    const messageId = crypto.randomUUID(), imageId = crypto.randomUUID();
    const stored: CachedUserMessageDraft = { id: messageId, text: 'Retained note', recipient: 'chief', uncertain: false,
      images: [{ id: imageId, name: 'saved.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    let finishRead!: (draft: CachedUserMessageDraft) => void;
    const read = new Promise<CachedUserMessageDraft>(resolve => { finishRead = resolve; });
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'user_message_draft_read') return read;
      if (command === 'user_message_image_read') throw new Error('Synthetic drop read failure');
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('user_message_list', () => ({ event: 'user_message_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ message_id: messageId, attachment_id: imageId, name: 'saved.png', state: 'ready', next_offset: 8 }] } } }));
    });
    expect(editor()).toHaveAttribute('readonly');
    await gesture(daemon, () => {
      if (source === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File(['image'], 'new.png', { type: 'image/png' })] } });
      else return native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } });
    });
    expect(screen.getByRole('alert')).toHaveTextContent('Draft is still loading');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'user_message_image_read')).toHaveLength(0);
    expect(daemon.sentOf('user_message_attachment_put')).toEqual([]);
    await act(async () => finishRead(stored)); await daemon.idle();
    expect(editor()).toHaveValue('Retained note'); expect(editor()).not.toHaveAttribute('readonly');
    expect(screen.getByRole('img', { name: 'saved.png' })).toBeInTheDocument();
    if (source === 'drop') {
      await gesture(daemon, () => native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } }));
      expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'user_message_image_read')).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent('Synthetic drop read failure');
    }
  });

});
