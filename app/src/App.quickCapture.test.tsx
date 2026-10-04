import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { QuickCapture } from './components/QuickCapture';
import { QUICK_CAPTURE_READY, QUICK_CAPTURE_REQUEST } from './quickCapture/client';
import { DEFAULT_PROFILE_ID, defaultProfile, emptyDesktop } from './test/daemonFixtures';
import type { CachedQuickCaptureDraft } from './quickCapture/draft';
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
  files: new Map<string, string>(),
  visible: false,
  readyGate: undefined as Promise<void> | undefined,
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
      if (event === 'attn://capture/ready' && native.readyGate) await native.readyGate;
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
  native.listeners.clear(); native.readyGate = undefined; native.visible = false; native.draft = null; native.drafts.clear(); native.files.clear(); native.binding = null; native.active = null;
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command, args) => {
    const values = args as Record<string, any> | undefined;
    if (command === 'get_build_instance') return { instance: 'capture-test' };
    if (command === 'get_client_token') return 'test-token';
    if (command === 'capture_hide') { native.onHide?.(); return; }
    if (command === 'capture_status') return { binding: native.binding, active: native.active };
    if (command === 'capture_bind') { native.active = values!.binding; return; }
    if (command === 'capture_cache') { native.binding = values!.binding; return; }
    if (command === 'quick_capture_draft_read') {
      const draft = (native.drafts.get(values!.profileId) ?? (native.drafts.size === 0 ? native.draft : null)) as CachedQuickCaptureDraft | null;
      for (const file of draft?.files ?? []) if (file.url) native.files.set(`${values!.profileId}:${file.id}`, file.url);
      return draft && { ...draft, files: draft.files.map(file => ({ ...file, url: file.url || native.files.get(`${values!.profileId}:${file.id}`)! })) };
    }
    if (command === 'quick_capture_draft_write') { native.draft = values!.draft; native.drafts.set(values!.profileId, values!.draft); return; }
    if (command === 'quick_capture_draft_file_write') { native.files.set(`${values!.profileId}:${values!.id}`, values!.url); return; }
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
    for (const listener of native.listeners.get(QUICK_CAPTURE_READY) ?? []) await listener({ payload: undefined });
  });
  await view.daemon.idle();
  return { ...view, hidden, capture };
}
const editor = () => screen.getByRole('textbox', { name: 'Message' });
type WireRecord = NonNullable<NonNullable<EventMessage<'quick_capture_result'>['result']>['record']>;
function record(command: { capture_id: string; content?: string; mailbox?: WireRecord['mailbox'] }): WireRecord {
  return { id: command.capture_id, content: command.content ?? 'saved message', mailbox: command.mailbox ?? { kind: 'chief' },
    attachments: [], created_at: '2026-10-01T12:00:00Z' };
}
function quickCaptureTraffic(daemon: ScriptedDaemon) { return daemon.sent.filter(command => command.cmd.startsWith('quick_capture_')); }

describe('Quick Capture app wire behavior', () => {
  it('connects when the capture starts before the main ready listener is registered', async () => {
    let registerReady!: () => void;
    native.readyGate = new Promise<void>(resolve => { registerReady = resolve; });
    const { daemon } = await captureApp();
    expect(screen.getByText('Connecting Quick Capture…')).toBeInTheDocument();
    await act(async () => registerReady());
    await daemon.idle();
    expect(editor()).toBeInTheDocument();
  });

  it('keeps edits made after returning to a profile when an older resolve finishes', async () => {
    const a = defaultProfile('desktop-1');
    const b = defaultProfile('desktop-work', { id: 'profile-work', name: 'Work' });
    const desktops = [emptyDesktop('desktop-1'), emptyDesktop('desktop-work', { profile_id: b.id })];
    native.draft = { id: crypto.randomUUID(), text: 'Retained A', mailbox: 'chief', files: [], uncertain: true };
    let resolutions = 0;
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_get', command => {
        resolutions++;
        if (resolutions === 2) return;
        return { event: 'quick_capture_result', profile_id: command.profile_id!, success: false,
          error: resolutions === 1 ? 'Storage is temporarily unavailable' : 'Quick capture absent',
          ...(resolutions === 1 ? {} : { error_code: 'quick_capture_not_found' }) };
      });
    }, { initialState: { profiles: [a, b], desktops, selected_profile_id: a.id } });
    await daemon.idle();
    expect(resolutions).toBe(1);
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(resolutions).toBe(2);
    const held = daemon.sentOf('quick_capture_get')[1];
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: a, desktops: [desktops[0]] }));
    await daemon.idle();
    expect(resolutions).toBe(3);
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Edited after returning to A' } }));
    await act(async () => daemon.replyTo(held, { event: 'quick_capture_result', profile_id: a.id, request_id: held.request_id, success: false, error: 'Quick capture absent', error_code: 'quick_capture_not_found' }));
    await daemon.idle();
    expect(editor()).toHaveValue('Edited after returning to A');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'Edited after returning to A', uncertain: false });
    expect(daemon.sentOf('quick_capture_send')).toEqual([]);
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
    const submission = await daemon.received('quick_capture_send');
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'B draft' } }));
    await act(async () => daemon.replyTo(submission, { event: 'quick_capture_result', profile_id: a.id, request_id: submission.request_id, success: true, result: { record: record(submission) } }));
    await daemon.idle();
    expect(hides).toBe(0);
    expect(editor()).toHaveValue('B draft');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'A request', uncertain: true });
    daemon.on('quick_capture_get', command => ({ event: 'quick_capture_result', profile_id: command.profile_id!, success: true, result: { record: record(submission) } }));
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: a, desktops: [desktops[0]] }));
    const reconciliation = await daemon.received('quick_capture_get');
    expect(reconciliation).toMatchObject({ profile_id: a.id, capture_id: submission.capture_id });
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
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'quick_capture_file_read' ? url : nativeInvoke(command, args));
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_list', command => ({ event: 'quick_capture_result', profile_id: command.profile_id!, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('quick_capture_attachment_put', command => command.offset === 0 ? undefined : ({ event: 'quick_capture_result', profile_id: other, success: false, error: 'The selected profile changed' }));
    }, { initialState: { profiles: [a, b], desktops, selected_profile_id: a.id } });
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'A retained draft' } }));
    await act(async () => { await native.onDrop!({ payload: { type: 'drop', paths: ['/a.pdf'], position: { x: 100, y: 100 } } }); });
    const first = await daemon.received('quick_capture_attachment_put');
    expect(first).toMatchObject({ profile_id: a.id, offset: 0, final: false });
    await act(async () => daemon.emit({ event: 'profile_arrangement_changed', profile: b, desktops: [desktops[1]] }));
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(screen.queryByRole('img', { name: 'a.pdf' })).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'B retained draft' } }));
    await act(async () => daemon.replyTo(first, { event: 'quick_capture_result', profile_id: a.id, request_id: first.request_id, success: true, result: { upload: { next_offset: 524288 } } }));
    await daemon.idle();
    expect(daemon.sentOf('quick_capture_attachment_put')).toHaveLength(2);
    expect(daemon.sentOf('quick_capture_attachment_put')[1]).toMatchObject({ profile_id: a.id, offset: 524288, final: true });
    expect(daemon.sentOf('quick_capture_send')).toEqual([]);
    expect(editor()).toHaveValue('B retained draft');
    expect(native.drafts.get(a.id)).toMatchObject({ text: 'A retained draft', files: [{ name: 'a.pdf' }] });
    expect(native.drafts.get(b.id)).toMatchObject({ text: 'B retained draft', files: [] });
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
    const { daemon, hidden } = await captureApp(daemon => daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } })));
    expect(screen.queryByRole('button', { name: 'Shortcut settings' })).not.toBeInTheDocument();
    expect(screen.queryByText('Open capture from anywhere')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Keep the launch note' } }));
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('quick_capture_send');
    expect(request).toMatchObject({ mailbox: { kind: 'chief' }, content: 'Keep the launch note', attachment_ids: [] });
    expect(editor()).toHaveValue('Keep the launch note');
    expect(screen.getByRole('button', { name: /^Retry/ })).toBeDisabled();
    expect(native.draft).toMatchObject({ id: request.capture_id, uncertain: true });
    await act(async () => { daemon.replyTo(request, { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, request_id: request.request_id, success: true, result: { record: record(request) } }); await hidden; });
    await daemon.idle();
    expect(editor()).toHaveValue('');
    expect(native.draft).toMatchObject({ text: '', mailbox: 'chief', uncertain: false });
    expect(daemon.sentOf('quick_capture_send')).toHaveLength(1);
    expect(quickCaptureTraffic(daemon).map(command => command.cmd)).toEqual(['quick_capture_send', 'quick_capture_list']);
    expect(screen.getByText('Saved for Chief')).toBeInTheDocument();
    daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: {
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
    const request = await daemon.received('quick_capture_send');
    daemon.on('quick_capture_get', command => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record({ ...request, capture_id: command.capture_id }) } }));
    await act(async () => daemon.disconnect(1009, 'WebSocket max_message_bytes=1048576, received at least 1048577'));
    await daemon.idle();
    expect(editor()).toHaveValue('Uncertain note');
    expect(screen.getByRole('alert')).toHaveTextContent('max_message_bytes=1048576, received at least 1048577');
    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('quick_capture_get').map(command => command.capture_id)).toEqual([request.capture_id]);
    expect(editor()).toHaveValue('');
    expect(daemon.sentOf('quick_capture_send')).toHaveLength(1);
  });

  it('uploads restored image bytes before sending and retains the image on an upload failure', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    const url = 'data:image/png;base64,iVBORw0KGgo=';
    native.draft = { id: captureId, text: '', mailbox: 'chief', uncertain: false,
      files: [{ id: imageId, name: 'screenshot.png', url }] };
    const { daemon } = await captureApp(daemon => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).startsWith('data:')) return localFetch(input, init);
      throw new Error('app wire tests reach no network');
    });
    daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
    daemon.on('quick_capture_attachment_put', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Upload storage unavailable' }));
    daemon.on('quick_capture_get', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Quick capture absent', error_code: 'quick_capture_not_found' }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const upload = await daemon.received('quick_capture_attachment_put');
    await daemon.idle();
    expect(upload).toMatchObject({ capture_id: captureId, attachment_id: imageId, name: 'screenshot.png',
      offset: 0, data_base64: 'iVBORw0KGgo=', final: true });
    expect(daemon.sentOf('quick_capture_send')).toEqual([]);
    expect(screen.getByRole('img', { name: 'screenshot.png' })).toBeInTheDocument();
    expect(native.draft).toMatchObject({ id: captureId, files: [{ id: imageId }] });
  });

  it('reconciles a ready image after relaunch and sends an image-only capture without reuploading it', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: '', mailbox: 'chief', uncertain: false,
      files: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon, hidden } = await captureApp(daemon => {
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const request = await daemon.received('quick_capture_send');
    expect(request).toMatchObject({ capture_id: captureId, content: '', attachment_ids: [imageId] });
    expect(daemon.sentOf('quick_capture_attachment_put')).toEqual([]);
    await act(async () => { daemon.replyTo(request, { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, request_id: request.request_id,
      success: true, result: { record: record(request) } }); await hidden; });
    expect(screen.queryByRole('img', { name: 'kept.png' })).toBeNull();
  });

  it('drains an eager image upload before deleting a removed attachment', async () => {
    const captureId = crypto.randomUUID(); const imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: '', mailbox: 'chief', uncertain: false,
      files: [{ id: imageId, name: 'remove.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('quick_capture_attachment_discard', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }));
    });
    const upload = await daemon.received('quick_capture_attachment_put');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove remove.png' })));
    expect(daemon.sentOf('quick_capture_attachment_discard')).toEqual([]);
    await act(async () => { daemon.replyTo(upload, { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, request_id: upload.request_id,
      success: true, result: { upload: { next_offset: 8 } } }); });
    const discarded = await daemon.received('quick_capture_attachment_discard');
    await daemon.idle();
    expect(discarded).toMatchObject({ capture_id: captureId, attachment_id: imageId });
    expect(screen.queryByRole('img', { name: 'remove.png' })).toBeNull();
    expect(daemon.sentOf('quick_capture_send')).toEqual([]);
    expect(native.draft).toMatchObject({ files: [] });
  });

  it('IME Enter keeps composing, while Shift+Enter does not send', async () => {
    const { daemon } = await captureApp();
    await gesture(daemon, () => {
      fireEvent.change(editor(), { target: { value: '日本語' } });
      fireEvent.compositionStart(editor()); fireEvent.keyDown(editor(), { key: 'Enter', isComposing: true, keyCode: 229 });
      fireEvent.compositionEnd(editor()); fireEvent.keyDown(editor(), { key: 'Enter', shiftKey: true });
    });
    expect(editor()).toHaveValue('日本語');
    expect(quickCaptureTraffic(daemon)).toEqual([]);
  });

  it.each(['paste', 'drop'] as const)('attaches a PDF by %s, stages its bytes and submits without image decoding', async mode => {
    const pdf = '%PDF-1.4\nQuick Capture PDF fixture\n%%EOF\n';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'quick_capture_file_read'
      ? `data:application/pdf;base64,${btoa(pdf)}` : nativeInvoke(command, args));
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('quick_capture_attachment_put', command => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('quick_capture_send', command => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record(command) } }));
    });
    await act(async () => {
      if (mode === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File([pdf], 'notes.pdf', { type: 'application/pdf' })] } });
      else await native.onDrop!({ payload: { type: 'drop', paths: ['/fixture/notes.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('quick_capture_attachment_put');
    await daemon.idle();
    expect(atob(upload.data_base64)).toBe(pdf);
    expect(upload.name).toBe('notes.pdf');
    expect(screen.getByText('notes.pdf')).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'notes.pdf' })).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    expect(daemon.sentOf('quick_capture_send')).toMatchObject([{ capture_id: upload.capture_id, attachment_ids: [upload.attachment_id] }]);
  });

  it('retains edits and removal made while another file occupies the upload queue', async () => {
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => command === 'quick_capture_file_read'
      ? 'data:application/pdf;base64,JVBERi0xLjQK' : nativeInvoke(command, args));
    let uploadsHeld = true;
    const { daemon } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('quick_capture_attachment_put', command => uploadsHeld ? undefined : ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
      daemon.on('quick_capture_attachment_discard', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }));
    });
    await act(async () => {
      fireEvent.change(editor(), { target: { value: 'Before upload' } });
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf', '/second.pdf'], position: { x: 100, y: 100 } } });
    });
    const upload = await daemon.received('quick_capture_attachment_put', command => command.name === 'first.pdf');
    const secondUpload = await daemon.received('quick_capture_attachment_put', command => command.name === 'second.pdf');
    await gesture(daemon, () => fireEvent.change(editor(), { target: { value: 'Edited while uploading' } }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove first.pdf' })));
    uploadsHeld = false;
    await act(async () => { daemon.replyTo(upload, { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, request_id: upload.request_id, success: true,
      result: { upload: { next_offset: atob(upload.data_base64).length } } }); });
    await act(async () => daemon.replyTo(secondUpload, { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, request_id: secondUpload.request_id, success: true, result: { upload: { next_offset: atob(secondUpload.data_base64).length } } }));
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    const retained = native.draft as CachedQuickCaptureDraft;
    expect(retained.text).toBe('Edited while uploading');
    expect(retained.files.map(file => file.name)).toEqual(['second.pdf']);
  });

  it('retains the draft and resumes file staging after a disconnect drains the upload queue', async () => {
    const reads: string[] = [];
    const url = 'data:application/pdf;base64,JVBERi0xLjQK';
    const nativeInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command !== 'quick_capture_file_read') return nativeInvoke(command, args);
      reads.push((args as { path: string }).path); return url;
    });
    let held = true;
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: [] } } }));
      daemon.on('quick_capture_attachment_put', command => held ? undefined : ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true,
        result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
    });
    await act(async () => {
      await native.onDrop!({ payload: { type: 'drop', paths: ['/first.pdf'], position: { x: 100, y: 100 } } });
    });
    await daemon.received('quick_capture_attachment_put');
    await act(async () => {
      await native.onDrop!({ payload: { type: 'drop', paths: ['/second.pdf'], position: { x: 100, y: 100 } } });
      daemon.disconnect();
    });
    await daemon.idle();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(editor()).toBeInTheDocument();
    expect(reads).toEqual(['/first.pdf', '/second.pdf']);
    expect((native.draft as CachedQuickCaptureDraft).files.map(file => file.name)).toEqual(['first.pdf', 'second.pdf']);
    held = false;
    await daemon.reconnect();
    await daemon.received('quick_capture_attachment_put', command => command.name === 'second.pdf');
    await act(async () => {
      for (const listener of native.listeners.get('attn://capture/automation') ?? []) await listener({ payload: {
        request_id: crypto.randomUUID(), action: 'capture_state', payload: { staged: true },
      } });
    });
    expect(daemon.sentOf('quick_capture_attachment_put').filter(command => command.name === 'first.pdf')).toHaveLength(2);
    expect(daemon.sentOf('quick_capture_attachment_put').filter(command => command.name === 'second.pdf')).toHaveLength(1);
  });

  it.each([{ length: 0, offset: 0 }, { length: 524291, offset: 0 }, { length: 524292, offset: 1 }, { length: 524293, offset: 2 }])(
    'uploads exact retained bytes (length=$length, resumed offset=$offset) without a whole-file URL fetch', async ({ length, offset }) => {
      const captureId = crypto.randomUUID(), attachmentId = crypto.randomUUID();
      const bytes = Uint8Array.from({ length }, (_, index) => index % 256);
      const binary = Array.from(bytes, byte => String.fromCharCode(byte)).join('');
      native.draft = { id: captureId, text: 'Binary attachment', mailbox: 'chief', uncertain: false,
        files: [{ id: attachmentId, name: 'retained.bin', url: `data:application/octet-stream;base64,${btoa(binary)}` }] };
      const { daemon } = await captureApp(daemon => {
        vi.stubGlobal('fetch', () => { throw new Error('retained files upload directly from their base64 bytes'); });
        daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
          draft_assets: offset ? [{ capture_id: captureId, attachment_id: attachmentId, name: 'retained.bin', state: 'staged', next_offset: offset }] : [] } } }));
        daemon.on('quick_capture_attachment_put', command => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true,
          result: { upload: { next_offset: command.offset + atob(command.data_base64).length } } }));
        daemon.on('quick_capture_send', command => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { record: record(command) } }));
      });
      await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
      const uploads = daemon.sentOf('quick_capture_attachment_put');
      expect(uploads[0].offset).toBe(offset);
      expect(uploads.map(command => atob(command.data_base64)).join('')).toBe(binary.slice(offset));
      expect(uploads[uploads.length - 1].final).toBe(true);
      expect(daemon.sentOf('quick_capture_send')[0].attachment_ids).toEqual([attachmentId]);
    });

  it('Recent shows sent/read history and applies read receipts without fetching or reading the inbox', async () => {
    const firstId = crypto.randomUUID();
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [
        record({ capture_id: firstId, content: 'Sent message' }),
        { ...record({ capture_id: crypto.randomUUID(), content: 'Read message' }), read_at: '2026-10-01T12:01:00Z',
          attachments: [{ id: crypto.randomUUID(), name: 'notes.pdf', media_type: 'application/pdf', bytes: 51 }] },
      ], draft_assets: [] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    const recent = screen.getByRole('region', { name: 'Recent messages' });
    expect(within(recent).getByText('Sent message')).toBeInTheDocument();
    expect(within(recent).getByText('Read message')).toBeInTheDocument();
    expect(within(recent).getByText('notes.pdf')).toBeInTheDocument();
    expect(within(recent).getByText('PDF', { exact: true })).toBeInTheDocument();
    expect(within(recent).getByText('51 B')).toBeInTheDocument();
    expect(within(recent).getByText(/^Sent$/)).toBeInTheDocument();
    expect(within(recent).getByText(/^Read$/)).toBeInTheDocument();
    expect(within(recent).queryByRole('button', { name: /Cancel|Restore|Retry delivery|Open mailbox|Follow up/ })).toBeNull();
    expect(within(recent).queryByRole('combobox')).toBeNull();
    expect(daemon.sentOf('agent_inbox')).toEqual([]);
    expect(quickCaptureTraffic(daemon).map(command => command.cmd)).toEqual(['quick_capture_list']);

    await act(async () => {
      daemon.emit({ event: 'quick_capture_read', profile_id: DEFAULT_PROFILE_ID, capture_id: firstId, read_at: '2026-10-01T12:02:00Z' });
      daemon.emit({ event: 'quick_capture_read', profile_id: DEFAULT_PROFILE_ID, capture_id: crypto.randomUUID(), read_at: '2026-10-01T12:02:00Z' });
    });
    await daemon.idle();
    expect(daemon.sentOf('quick_capture_list')).toHaveLength(1);
    expect(within(recent).getAllByText(/^Read$/)).toHaveLength(2);
    daemon.on('quick_capture_list', () => undefined);
    await daemon.reconnect();
    const refresh = daemon.sentOf('quick_capture_list').at(-1)!;
    expect(daemon.sentOf('quick_capture_list')).toHaveLength(2);
    await act(async () => {
      daemon.emit({ event: 'quick_capture_read', profile_id: DEFAULT_PROFILE_ID, capture_id: firstId, read_at: '2026-10-01T12:03:00Z' });
      daemon.replyTo(refresh, { event: 'quick_capture_result', request_id: refresh.request_id, profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: {
        items: [record({ capture_id: firstId, content: 'Sent message' })], draft_assets: [],
      } } });
    });
    await daemon.idle();
    expect(within(recent).getByText(/^Read$/)).toBeInTheDocument();
    expect(within(recent).queryByText(/^Sent$/)).toBeNull();

  });

  it('resends retained image bytes after a failed draft discard', async () => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID(), secondId = crypto.randomUUID();
    native.draft = { id: captureId, text: 'Keep this message', mailbox: 'chief', uncertain: false,
      files: [imageId, secondId].map(id => ({ id, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' })) };
    let discarded = false;
    const { daemon, capture } = await captureApp(daemon => {
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith('data:')) return localFetch(input, init);
        throw new Error('app wire tests reach no network');
      });
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [], draft_assets: discarded ? [] : [imageId, secondId].map(id => ({ capture_id: captureId, attachment_id: id, name: 'kept.png', state: 'ready', next_offset: 8 })) } } }));
      daemon.on('quick_capture_attachment_discard', command => {
        expect(native.draft).toMatchObject({ text: 'Keep this message' });
        expect((native.draft as { id: string }).id).not.toBe(captureId);
        discarded = true; return command.attachment_id === imageId
        ? { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { discarded: true } }
        : { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Discard acknowledgment lost' }; });
      daemon.on('quick_capture_attachment_put', command => command.capture_id === captureId
        ? { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: false, error: 'Attachment was discarded; upload with a new identity' }
        : { event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { upload: { next_offset: 8 } } });
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Discard acknowledgment lost');
    capture.unmount();
    render(<QuickCapture />);
    await act(async () => {
      for (const listener of native.listeners.get(QUICK_CAPTURE_READY) ?? []) await listener({ payload: undefined });
    });
    await daemon.idle();
    expect(editor()).toHaveValue('Keep this message');
    await gesture(daemon, () => fireEvent.keyDown(editor(), { key: 'Enter' }));
    const sent = await daemon.received('quick_capture_send');
    expect(sent).toMatchObject({ content: 'Keep this message', attachment_ids: [imageId, secondId] });
    expect(sent.capture_id).not.toBe(captureId);
    expect(daemon.sentOf('quick_capture_attachment_put')).toMatchObject([imageId, secondId].map(id => ({ capture_id: sent.capture_id, attachment_id: id, data_base64: 'iVBORw0KGgo=' })));
    expect(native.draft).toMatchObject({ id: expect.any(String) });
  });

  it('keeps the draft without remote deletion when its replacement identity cannot be saved', async () => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID();
    native.draft = { id: captureId, text: 'Keep this message', mailbox: 'chief', uncertain: false,
      files: [{ id: imageId, name: 'kept.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'quick_capture_draft_write' && (args as { draft: CachedQuickCaptureDraft }).draft.id !== captureId) {
        throw new Error('Local draft storage unavailable');
      }
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'kept.png', state: 'ready', next_offset: 8 }] } } }));
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Recent messages' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Discard draft' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Local draft storage unavailable');
    expect(daemon.sentOf('quick_capture_attachment_discard')).toEqual([]);
    expect(native.draft).toMatchObject({ id: captureId, text: 'Keep this message' });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Back to draft/ })));
    expect(editor()).toHaveValue('Keep this message');
    expect(screen.getByRole('img', { name: 'kept.png' })).toBeInTheDocument();
  });

  it.each(['paste', 'drop'])('blocks %s files until the retained draft is restored', async source => {
    const captureId = crypto.randomUUID(), imageId = crypto.randomUUID();
    const stored: CachedQuickCaptureDraft = { id: captureId, text: 'Retained message', mailbox: 'chief', uncertain: false,
      files: [{ id: imageId, name: 'saved.png', url: 'data:image/png;base64,iVBORw0KGgo=' }] };
    const originalInvoke = vi.mocked(invoke).getMockImplementation()!;
    let finishRead!: (draft: CachedQuickCaptureDraft) => void;
    const read = new Promise<CachedQuickCaptureDraft>(resolve => { finishRead = resolve; });
    vi.mocked(invoke).mockImplementation(async (command, args) => {
      if (command === 'quick_capture_draft_read') return read;
      if (command === 'quick_capture_file_read') throw new Error('Synthetic drop read failure');
      return originalInvoke(command, args);
    });
    const { daemon } = await captureApp(daemon => {
      daemon.on('quick_capture_list', () => ({ event: 'quick_capture_result', profile_id: DEFAULT_PROFILE_ID, success: true, result: { list: { items: [],
        draft_assets: [{ capture_id: captureId, attachment_id: imageId, name: 'saved.png', state: 'ready', next_offset: 8 }] } } }));
    });
    expect(editor()).toHaveAttribute('readonly');
    await gesture(daemon, () => {
      if (source === 'paste') fireEvent.paste(editor(), { clipboardData: { files: [new File(['image'], 'new.png', { type: 'image/png' })] } });
      else return native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } });
    });
    expect(screen.getByRole('alert')).toHaveTextContent('Draft is still loading');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'quick_capture_file_read')).toHaveLength(0);
    expect(daemon.sentOf('quick_capture_attachment_put')).toEqual([]);
    await act(async () => finishRead(stored)); await daemon.idle();
    expect(editor()).toHaveValue('Retained message'); expect(editor()).not.toHaveAttribute('readonly');
    expect(screen.getByRole('img', { name: 'saved.png' })).toBeInTheDocument();
    if (source === 'drop') {
      await gesture(daemon, () => native.onDrop!({ payload: { type: 'drop', paths: ['/synthetic/new.png'], position: { x: 0, y: 0 } } }));
      expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'quick_capture_file_read')).toHaveLength(1);
      expect(screen.getByRole('alert')).toHaveTextContent('Synthetic drop read failure');
    }
  });

});
