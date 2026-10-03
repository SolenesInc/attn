import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { emit, listen } from '@tauri-apps/api/event';
import { getCurrentWebviewWindow } from '@tauri-apps/api/webviewWindow';
import { hideBootSplash } from '../utils/bootSplash';
import { readImage } from '@tauri-apps/plugin-clipboard-manager';
import { CaptureAttachmentPreview, type CaptureAttachment, type AttachmentOrigin, type AttachmentMotion } from './CaptureAttachmentPreview';
import './QuickCapture.css';
import { QuickCaptureHistory } from './QuickCaptureHistory';
import { createCaptureBridge, EMPTY_HOST_STATE, type CaptureClient, type CaptureHostState, type CaptureItem, type CaptureDraft } from '../quickCapture/client';
import { CaptureWorkQueue } from '../quickCapture/workQueue';
import { captureDraftCache, newCaptureDraft } from '../quickCapture/draft';
import { useShortcut } from '../shortcuts/useShortcut';
import { parseKeybindingsConfig, setShortcutOverrides } from '../shortcuts/resolver';

const automationEnabled = (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
type Attachment = CaptureAttachment;
type CaptureExpectation = { frame?: boolean; visible?: boolean; settled?: boolean; imageCount?: number; attachmentCount?: number; view?: 'compose' | 'recent'; recentText?: string; composing?: boolean; recipient?: string; fontScale?: number };


export function QuickCapture({ client: suppliedClient, hostState }: { client?: CaptureClient; hostState?: CaptureHostState } = {}) {
  const [host, setHost] = useState(hostState ?? EMPTY_HOST_STATE);
  const bridge = useRef<ReturnType<typeof createCaptureBridge> | null>(null);
  const client = useRef(suppliedClient);
  const [draftCache] = useState(captureDraftCache);
  const [workQueue] = useState(() => new CaptureWorkQueue());
  const cache = useRef(draftCache);
  const [initialDraft] = useState(newCaptureDraft);
  const identity = useRef(initialDraft);
  const [restored, setRestored] = useState(false);
  const fontScale = host.fontScale ?? 1;
  useEffect(() => { setShortcutOverrides(parseKeybindingsConfig(host.keybindings).overrides); }, [host.keybindings]);
  const resizeText = (action: 'increase' | 'decrease' | 'reset') => {
    void client.current?.resizeText(action).catch(error => setError(String(error)));
  };
  useShortcut('ui.increaseFontSize', () => resizeText('increase'), restored);
  useShortcut('ui.decreaseFontSize', () => resizeText('decrease'), restored);
  useShortcut('ui.resetFontSize', () => resizeText('reset'), restored);
  const [uncertain, setUncertain] = useState(false);
  const [resolving, setResolving] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [recent, setRecent] = useState(false);
  const recentGeneration = useRef(0);
  const loadingPage = useRef(false);
  const [loadingRecent, setLoadingRecent] = useState(false);
  const [nextCursor, setNextCursor] = useState<string>();
  const [history, setHistory] = useState<CaptureItem[]>([]);
  const [saved, setSaved] = useState<CaptureItem[]>([]);
  const roster = host.recipients;
  const label = (id: string) => roster.find(item => item.id === id)?.name ?? id;
  const [text, setText] = useState('');
  const [recipient, setRecipient] = useState('chief');
  const [images, setImages] = useState<Attachment[]>([]);
  const [error, setError] = useState('');
  const binding = host.binding ?? '';
  const [dragging, setDragging] = useState(false);
  const [picker, setPicker] = useState(false);
  const [pick, setPick] = useState(0);
  const recipientMenu = useRef<HTMLDivElement>(null);
  const ingestion = useRef<{ startedAt: number; readyAt?: number; count: number }[]>([]);
  const motion = useRef<(AttachmentMotion | { kind: 'drop'; phase: 'start' | 'end'; at: number })[]>([]);
  const visible = useRef(false);
  const viewRecent = useRef(recent);
  useLayoutEffect(() => { viewRecent.current = recent; }, [recent]);
  const draftGeneration = useRef(0);
  const ownedImages = useRef(new Set<string>());
  const stagedImages = useRef(new Map<string, Promise<void>>());
  const onMotion = useCallback((receipt: AttachmentMotion) => { if (automationEnabled) motion.current.push(receipt); }, []);
  const onSettled = useCallback((id: string) => { setImages(previous => previous.map(image => image.id === id ? { ...image, arriving: false } : image)); }, []);
  function settleEntrances() { setImages(previous => previous.map(image => ({ ...image, arriving: false }))); }
  const latency = useRef<{ openedAt: number; focusedAt: number; nativeShowToFocusMs: number }[]>([]);
  const editor = useRef<HTMLTextAreaElement>(null);
  const historyView = useRef<HTMLElement>(null);
  const noteSelection = useRef<{ start: number; end: number; direction: 'forward' | 'backward' | 'none' }>(null);
  const composing = useRef(false);
  const sending = useRef(false);
  const state = useRef({ text, recipient, images, saved, binding, uncertain, restored });
  useLayoutEffect(() => { state.current = { text, recipient, images, saved, binding, uncertain, restored }; }, [text, recipient, images, saved, binding, uncertain, restored]);

  function addImages(items: { name: string; load: () => Promise<string> }[], origin: AttachmentOrigin, silent = false) {
    if (!state.current.restored) { setError('Draft is still loading. Try adding the file again when it is ready.'); return; }
    setSaved([]);
    const generation = draftGeneration.current;
    const arrivals = items.map(item => ({ id: crypto.randomUUID(), name: item.name, url: '', origin, ready: false,
      arriving: visible.current && !viewRecent.current && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches }));
    arrivals.forEach(image => ownedImages.current.add(image.id));
    setImages(previous => [...previous, ...arrivals]);
    const measurement = { startedAt: Date.now(), count: arrivals.length, readyAt: undefined as number | undefined };
    let remaining = arrivals.length;
    if (automationEnabled) ingestion.current.push(measurement);
    arrivals.forEach((image, index) => {
      void workQueue.run(async () => {
        if (draftGeneration.current !== generation || !ownedImages.current.has(image.id)) return;
        const url = await items[index].load();
        if (draftGeneration.current !== generation || !ownedImages.current.has(image.id)) return;
        setImages(previous => previous.map(item => item.id === image.id ? { ...item, url } : item));
        const imagePreview = url.startsWith('data:image/') && await ImagePromise(url).then(() => true, () => false);
        if (draftGeneration.current !== generation || !ownedImages.current.has(image.id)) return;
        setImages(previous => previous.map(item => item.id === image.id ? { ...item, url, ready: true, imagePreview,
          arriving: item.arriving && visible.current && !viewRecent.current } : item));
      }).finally(() => { if (--remaining === 0) measurement.readyAt = Date.now(); }).catch(error => {
        if (draftGeneration.current !== generation || !ownedImages.current.delete(image.id)) return;
        setImages(previous => previous.filter(item => item.id !== image.id));
        if (!silent) setError(`Cannot attach ${image.name}: ${error}`);
      });
    });
  }
  function addFiles(files: File[]) {
    const items = files.map(file => ({ name: file.name, load: () => fileDataUrl(file) }));
    addImages(items, { kind: 'paste' });
  }
  const removeImage = useCallback((id: string) => {
    if (sending.current || state.current.uncertain) return;
    ownedImages.current.delete(id); stagedImages.current.delete(id);
    const image = state.current.images.find(item => item.id === id);
    setImages(previous => previous.filter(item => item.id !== id));
    if (image?.url.startsWith('blob:')) URL.revokeObjectURL(image.url);
    void client.current!.discard(identity.current.id, [id]).catch(error => setError(String(error)));
    editor.current?.focus();
  }, []);
  function pasteNativeImage() {
    addImages([{ name: 'Pasted screenshot', load: async () => {
      const image = await readImage();
      try {
        const { width, height } = await image.size();
        const rgba = await image.rgba();
        const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height;
        canvas.getContext('2d')!.putImageData(new ImageData(new Uint8ClampedArray(rgba), width, height), 0, 0);
        return canvas.toDataURL('image/png');
      } finally { await image.close(); }
    } }], { kind: 'paste' }, true);
  }
  function draft(isUncertain = uncertain): CaptureDraft {
    return { id: identity.current.id, text: state.current.text, recipient: state.current.recipient,
      images: state.current.images.filter(image => image.url).map(({ id, name, url }) => ({ id, name, url })), uncertain: isUncertain };
  }
  async function refreshRecent(cursor?: string) {
    if (cursor && loadingPage.current) return;
    const generation = cursor ? recentGeneration.current : ++recentGeneration.current;
    loadingPage.current = true; setLoadingRecent(true);
    try {
      const result = await client.current!.recent(cursor);
      if (generation !== recentGeneration.current) return;
      setHistory(previous => cursor ? [...previous, ...result.items.filter(item => !previous.some(saved => saved.id === item.id))] : result.items);
      setNextCursor(result.nextCursor); setError('');
    } catch (error) { if (generation === recentGeneration.current) setError(String(error)); }
    finally { if (generation === recentGeneration.current) { loadingPage.current = false; setLoadingRecent(false); } }
  }
  function stageDraft(staged: CaptureDraft): Promise<void> {
    const fresh = staged.images.filter(image => !stagedImages.current.has(image.id));
    for (const image of fresh) {
      const pending = workQueue.run(async () => {
        if (identity.current.id !== staged.id || !ownedImages.current.has(image.id)) return;
        await cache.current.save(draft());
        if (identity.current.id !== staged.id || !ownedImages.current.has(image.id)) return;
        await client.current!.stage({ ...staged, images: [image] });
      });
      stagedImages.current.set(image.id, pending);
      void pending.catch(() => {
        if (stagedImages.current.get(image.id) === pending) stagedImages.current.delete(image.id);
      });
    }
    return Promise.all(staged.images.map(image => stagedImages.current.get(image.id))).then(() => {});
  }
  async function send() {
    if (sending.current || !restored || images.some(image => !image.ready) || (!text.trim() && images.length === 0)) return;
    sending.current = true; setSubmitting(true); settleEntrances();
    try {
      let accepted: CaptureItem | null = null;
      if (uncertain) {
        accepted = await client.current!.resolve(identity.current.id);
        if (!accepted) setUncertain(false);
      }
      if (!accepted) {
        const outgoing = draft(false);
        await cache.current.save(outgoing);
        if (outgoing.images.length) await stageDraft(outgoing);
        const pending = { ...outgoing, uncertain: true };
        await cache.current.save(pending);
        setUncertain(true);
        accepted = await client.current!.submit({ id: pending.id, text: pending.text, recipient: pending.recipient, imageIds: pending.images.map(image => image.id) });
      }
      await accept(accepted, true);
    } catch (error) { setError(String(error)); }
    finally { sending.current = false; setSubmitting(false); }
  }
  async function accept(accepted: CaptureItem, hide: boolean) {
    const next = newCaptureDraft(); await cache.current.save(next); identity.current = next;
    draftGeneration.current++; ownedImages.current.clear(); stagedImages.current.clear();
    setSaved([accepted]);
    setText(''); setImages([]); setRecipient('chief'); setUncertain(false); setError('');
    if (hide) await invoke('capture_hide');
  }
  async function resolveSubmission() {
    if (sending.current || !state.current.uncertain) return;
    sending.current = true; setResolving(true);
    try {
      const accepted = await client.current!.resolve(identity.current.id);
      if (accepted) await accept(accepted, false);
      else { await cache.current.save(draft(false)); setUncertain(false); setError('Not yet saved. You can edit or send this draft again.'); }
    } catch (error) { setError(String(error)); }
    finally { sending.current = false; setResolving(false); }
  }
  async function discardDraft() {
    if (sending.current) return;
    if (uncertain) { setError('Resolve this submission before discarding it. Use Retry to check whether it was saved.'); return; }
    sending.current = true; setSubmitting(true);
    try {
      const previousId = identity.current.id;
      const retained = { ...draft(), id: crypto.randomUUID() };
      await cache.current.save(retained); identity.current = retained; stagedImages.current.clear();
      await client.current!.discard(previousId, images.map(image => image.id));
      const next = newCaptureDraft(); await cache.current.save(next); identity.current = next;
      draftGeneration.current++; ownedImages.current.clear(); stagedImages.current.clear(); setImages([]); setText(''); setRecipient('chief');
    } catch (error) { stagedImages.current.clear(); setError(String(error)); }
    finally { sending.current = false; setSubmitting(false); }
  }

  useEffect(() => {
    if (suppliedClient) client.current = suppliedClient;
    else { bridge.current = createCaptureBridge(setHost); client.current = bridge.current.client; }
    let disposed = false;
    void cache.current.read().then(stored => {
      if (disposed) return;
      if (stored) {
        identity.current = stored; setText(stored.text); setRecipient(stored.recipient); setUncertain(stored.uncertain);
        setImages(stored.images.map(image => ({ ...image, ready: true, arriving: false, origin: { kind: 'paste' } })));
        stored.images.forEach(image => ownedImages.current.add(image.id));
      }
      setRestored(true);
    }, error => { if (!disposed) setError(`Cannot restore draft: ${error}`); });
    return () => { disposed = true; bridge.current?.dispose(); };
  }, [suppliedClient]);
  useEffect(() => { if (restored && uncertain && !submitting && host.connected) void resolveSubmission(); }, [restored, uncertain, submitting, host.connected]);
  useEffect(() => {
    if (!restored || !host.connected || uncertain || submitting || !images.length || images.some(image => !image.ready)) return;
    void stageDraft(draft()).catch(error => setError(`File upload: ${error}`));
  }, [images, restored, host.connected, uncertain, submitting]);
  useEffect(() => { if (recent && host.connected) void refreshRecent(); }, [recent, host.connected, host.captureRevision]);
  useEffect(() => { if (hostState) setHost(hostState); }, [hostState]);
  useEffect(() => {
    if (!restored || sending.current) return;
    void cache.current.save(draft()).catch(error => setError(`Draft could not be saved: ${error}`));
  }, [text, recipient, images, uncertain, restored]);

  useEffect(() => {
    hideBootSplash();
    document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "false";
    editor.current?.focus();
    const open = listen<number>('capture-open', ({ payload }) => { visible.current = true; document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "true"; setRecent(false); setPicker(false); void bridge.current?.refresh(); void resolveSubmission(); editor.current?.focus(); if (automationEnabled) latency.current.push({ openedAt: payload, focusedAt: Date.now(), nativeShowToFocusMs: Date.now() - payload }); });
    const hidden = listen("capture-hidden", () => { visible.current = false; setRecent(false); settleEntrances(); document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "false"; });
    const drop = getCurrentWebviewWindow().onDragDropEvent(async event => {
      setDragging(event.payload.type === 'enter' || event.payload.type === 'over');
      if (event.payload.type !== 'drop') return;
      // Wry 0.55.1's Mac host emits AppKit points despite the PhysicalPosition label.
      if (sending.current || state.current.uncertain) return;
      const origin: AttachmentOrigin = { kind: 'drop', x: event.payload.position.x, y: event.payload.position.y };
      addImages(event.payload.paths.map(path => ({ name: path.split('/').pop() || 'Dropped file', load: () => invoke<string>('capture_image_read', { path })
      })), origin);
    });
    const inputTrace: object[] = [];
    const traceInput = (event: Event) => inputTrace.push({ at: Date.now(), type: event.type,
      key: event instanceof KeyboardEvent ? event.key : undefined,
      data: event instanceof InputEvent ? event.data : undefined, focused: document.activeElement === editor.current,
      readOnly: editor.current?.readOnly, value: editor.current?.value });
    if (automationEnabled) ['keydown', 'beforeinput', 'input'].forEach(type => document.addEventListener(type, traceInput, true));
    const automation = automationEnabled ? listen<{ request_id: string; action: string; payload: { binding?: string; batchSize?: number; staged?: boolean } & CaptureExpectation }>('attn://capture/automation', async ({ payload }) => {
      if (!payload.action.startsWith('capture_')) return;
      let result: unknown;
      let error: string | undefined;
      try {
        if (payload.action === 'capture_state') {
          if (payload.payload.batchSize !== undefined) workQueue.setCapacity(payload.payload.batchSize);
          if (payload.payload.frame) await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
          if (payload.payload.attachmentCount !== undefined || payload.payload.imageCount !== undefined || payload.payload.view || payload.payload.recentText || payload.payload.visible !== undefined || payload.payload.composing !== undefined || payload.payload.recipient !== undefined || payload.payload.fontScale !== undefined) await waitForCaptureView(payload.payload);
          if (payload.payload.staged) { await stageDraft(draft()); await workQueue.whenIdle(); }
          const bounds = (element: Element | null) => { const rect = element?.getBoundingClientRect(); return rect ? { x: (rect.x + rect.width / 2) / window.innerWidth, y: (rect.y + rect.height / 2) / window.innerHeight } : null; };
          const nativeDiagnostics = await invoke<{ diagnostics?: string[] }>('capture_status');
          result = { ingestion: ingestion.current, work: workQueue.snapshot(), recentRows: [...document.querySelectorAll('.capture-history-item')].map(row => ({ text: row.textContent, buttons: [...row.querySelectorAll('button')].map(button => button.textContent) })), inputTrace, nativeDiagnostics: nativeDiagnostics.diagnostics, visibility: document.visibilityState, nativeFocused: await getCurrentWebviewWindow().isFocused(), fontScale: Number(document.querySelector<HTMLElement>('.capture')?.dataset.fontScale), editorFontSize: editor.current ? getComputedStyle(editor.current).fontSize : null, view: document.querySelector('.capture-history') ? 'recent' : 'compose', flyingImages: document.querySelectorAll('.capture-flight').length, controls: { recent: bounds(document.querySelector('[aria-label="Recent captures"]')), remove: bounds(document.querySelector('.capture-images button')), editor: bounds(editor.current), recipient: bounds(document.querySelector('[aria-label="Recipient"]')) }, ...state.current, composing: composing.current, images: state.current.images.map(({ name }) => ({ name })), motion: motion.current, reducedMotion: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false, activeAnimations: document.getAnimations().filter(animation => animation.playState === 'running').length, latencyMs: latency.current, focused: document.activeElement === editor.current, visible: await getCurrentWebviewWindow().isVisible() };

        } else if (payload.action === 'capture_binding') {
          await client.current!.setBinding(payload.payload.binding || null);
          result = { binding: payload.payload.binding || '' };
        } else if (payload.action === 'capture_dismiss') { await invoke('capture_hide'); result = {}; }
        else throw new Error(`Unknown capture action ${payload.action}`);
      } catch (e) { error = String(e); }
      await emit('attn://ui-automation/response', { request_id: payload.request_id, ok: !error, result, error });
    }) : Promise.resolve(() => {});
    return () => { ['keydown', 'beforeinput', 'input'].forEach(type => document.removeEventListener(type, traceInput, true)); draftGeneration.current++; ownedImages.current.clear(); stagedImages.current.clear(); state.current.images.forEach(image => { if (image.url.startsWith('blob:')) URL.revokeObjectURL(image.url); }); void open.then(f => f()); void hidden.then(f => f()); void drop.then(f => f()); void automation.then(f => f()); };
  }, []);

  useEffect(() => {
    if (picker) recipientMenu.current?.focus();
    else if (recent) historyView.current?.focus();
    else if (editor.current) {
      editor.current.focus();
      if (noteSelection.current) {
        const { start, end, direction } = noteSelection.current;
        editor.current.setSelectionRange(start, end, direction);
        noteSelection.current = null;
      }
    }
  }, [picker, recent]);

  function openPicker() { if (uncertain || submitting) return; setPick(Math.max(0, roster.findIndex(item => item.id === recipient))); setRecent(false); setPicker(!picker); }
  const receipt = submitting ? 'Saving…' : resolving ? 'Checking submission…' : uncertain ? 'Submission unconfirmed' : !host.connected ? 'Connecting…' : saved.length ? `Saved for ${label(saved[saved.length - 1].recipient)}` : '';
  return <main className="capture" data-recipient={automationEnabled ? recipient : undefined} data-font-scale={automationEnabled ? fontScale : undefined} style={{ '--ui-scale': fontScale } as CSSProperties} onKeyDown={event => {
    if (event.nativeEvent.isComposing || composing.current || event.keyCode === 229) return;
    if (event.metaKey && !event.altKey && !event.ctrlKey && !event.shiftKey && /^[1-4]$/.test(event.key)) {
      event.preventDefault(); if (uncertain || submitting) return; if (roster[Number(event.key) - 1]) setRecipient(roster[Number(event.key) - 1].id); setPicker(false);
      editor.current?.focus();
    }
    else if (event.metaKey && event.key.toLowerCase() === 'k') { event.preventDefault(); openPicker(); }
    else if (event.key === 'Escape') {
      event.preventDefault();
      if (picker) setPicker(false);
      else if (recent) setRecent(false);
      else void invoke('capture_hide');
    }
  }} onPointerDown={event => {
    if (picker && !(event.target as Element).closest('.capture-menu, .capture-recipient')) setPicker(false);
  }}>
    {!recent && <header className="capture-top" data-tauri-drag-region>
      <span>To</span>
      <button aria-label="Recent captures" className="capture-recent-toggle" aria-pressed={recent} onClick={() => {
        if (!recent && editor.current) noteSelection.current = { start: editor.current.selectionStart, end: editor.current.selectionEnd, direction: editor.current.selectionDirection };
        settleEntrances(); setRecent(!recent); setPicker(false);
      }}>Recent</button>
      <button aria-label="Recipient" disabled={uncertain || submitting} title="Choose recipient · ⌘K" className="capture-recipient" aria-haspopup="listbox" aria-expanded={picker} onClick={openPicker}>{label(recipient)} <ChevronIcon /></button>
    </header>}
    {recent ? <QuickCaptureHistory regionRef={historyView} client={client.current!} history={history}
      hasDraft={!!text || images.length > 0} submitting={submitting}
      loading={loadingRecent} nextCursor={nextCursor} label={label} onRefresh={refreshRecent}
      onBack={() => setRecent(false)} onDiscard={() => void discardDraft()} /> : <>
      <textarea autoFocus ref={editor} aria-label="Capture message" placeholder={`Message ${label(recipient)}`} value={text} readOnly={uncertain || submitting || !restored} onChange={event => { setSaved([]); setText(event.target.value); }} onCompositionStart={event => { composing.current = true; event.currentTarget.dataset.composing = "true"; }} onCompositionEnd={event => { composing.current = false; event.currentTarget.dataset.composing = "false"; }} onKeyDown={event => {
        if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && !composing.current && event.keyCode !== 229) { event.preventDefault(); void send(); }
      }} onPaste={event => {
        if (uncertain || sending.current) { event.preventDefault(); return; }
        const files = [...event.clipboardData.files];
        if (files.length) { event.preventDefault(); addFiles(files); }
        else void pasteNativeImage();
      }} />
      {images.length > 0 && <div className="capture-images">{images.map(image => <CaptureAttachmentPreview key={image.id} image={image} onSettled={onSettled} onMotion={onMotion} onRemove={removeImage} disabled={uncertain || submitting} />)}</div>}
    </>}
    {(host.connectionError || error || host.shortcutError) && <p role="alert" className="capture-error">{host.connectionError || error || host.shortcutError}</p>}
    {(!recent || receipt) && <footer className="capture-footer">
      {receipt && <span className="capture-receipt" aria-live="polite"><i />{receipt}</span>}
      {!recent && <span className="capture-keys"><kbd>⇧↵</kbd> new line</span>}
      {!recent && <button className="send-button" disabled={!restored || submitting || resolving || images.some(image => !image.ready) || (!text.trim() && !images.length)} onClick={() => void send()}>{uncertain ? 'Retry' : 'Send'} <kbd>↵</kbd></button>}
    </footer>}
    {picker && <div ref={recipientMenu} className="capture-menu" role="listbox" aria-label="Choose recipient" tabIndex={-1} aria-activedescendant={`recipient-${pick}`} onKeyDown={event => {
      if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
        event.preventDefault(); event.stopPropagation();
        if (event.key === 'ArrowDown') setPick((pick + 1) % roster.length);
        else if (event.key === 'ArrowUp') setPick((pick + roster.length - 1) % roster.length);
        else if (event.key === 'Home') setPick(0);
        else if (event.key === 'End') setPick(roster.length - 1);
        else { setRecipient(roster[pick].id); setPicker(false); }
      } else if (event.key === 'Tab') setPicker(false);
    }}>{roster.map((item, index) => <div id={`recipient-${index}`} role="option" aria-selected={index === pick} key={item.id} className={item.id === recipient ? 'chosen' : ''} onPointerMove={() => setPick(index)} onClick={() => { setRecipient(item.id); setPicker(false); }}>
      {item.name} <span>{item.detail}</span>{index < 4 && <kbd>⌘{index + 1}</kbd>}
    </div>)}</div>}
    {dragging && <div className="capture-drop" onAnimationStart={() => { if (automationEnabled) motion.current.push({ kind: 'drop', phase: 'start', at: Date.now() }); }} onAnimationEnd={() => { if (automationEnabled) motion.current.push({ kind: 'drop', phase: 'end', at: Date.now() }); }}>Drop to attach to this note</div>}
  </main>;
}

function ChevronIcon() { return <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M2 3.5l3 3 3-3" /></svg>; }


function ImagePromise(url: string): Promise<void> {
  return new Promise((resolve, reject) => { const image = new Image(); image.onload = () => resolve(); image.onerror = reject; image.src = url; });
}

function waitForCaptureView({ imageCount: count, attachmentCount, view, recentText, settled, visible, composing, recipient, fontScale }: CaptureExpectation): Promise<void> {
  return new Promise((resolve, reject) => {
    const finish = (error?: string) => { observer.disconnect(); document.removeEventListener('load', check, true); document.removeEventListener('error', check, true); document.removeEventListener('focusin', check, true); if (error) reject(new Error(error)); else resolve(); };
    const check = () => {
      const images = [...document.querySelectorAll<HTMLImageElement>('.capture-images img')];
      const error = document.querySelector('[role="alert"]')?.textContent;
      if (count !== undefined && (error || images.length > count)) { finish(`Expected ${count} decoded image previews, observed ${images.length}. ${error || ''}`); return; }
      const files = [...document.querySelectorAll<HTMLElement>('.capture-images figure')];
      const filesReady = attachmentCount === undefined || (files.length === attachmentCount && files.every(file => file.dataset.fileReady === 'true'));
      const target = document.querySelector('.capture textarea');
      const recentView = document.querySelector('.capture-history');
      const viewReady = !view || (view === 'recent' ? recentView?.contains(document.activeElement) : target && document.activeElement === target);
      const recentReady = !recentText || Boolean(document.querySelector('.capture-history-list')?.textContent?.includes(recentText));
      const imagesReady = count === undefined || (images.length === count && images.every(image => image.complete && image.naturalWidth > 0));
      const motionReady = !settled || !document.querySelector('.capture-images .arriving');
      const visibleReady = visible === undefined || document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible === String(visible);
      const recipientReady = recipient === undefined || document.querySelector<HTMLElement>('.capture')?.dataset.recipient === recipient;
      const fontReady = fontScale === undefined || Number(document.querySelector<HTMLElement>('.capture')?.dataset.fontScale) === fontScale;
      if (filesReady && recentReady && fontReady && viewReady && imagesReady && motionReady && visibleReady && recipientReady && (composing === undefined || (document.querySelector<HTMLElement>(".capture textarea")?.dataset.composing === "true") === composing)) finish();
    };
    const observer = new MutationObserver(check);
    observer.observe(document.querySelector('.capture')!, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'data-capture-visible', 'data-composing', 'data-recipient', 'data-font-scale', 'data-file-ready'] });
    document.addEventListener('load', check, true); document.addEventListener('error', check, true); document.addEventListener('focusin', check, true);
    check();
  });
}

function fileDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader(); reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error); reader.readAsDataURL(blob);
  });
}
