import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { getCurrentWebviewWindow } from '@tauri-apps/api/webviewWindow';
import { hideBootSplash } from '../utils/bootSplash';
import { readImage } from '@tauri-apps/plugin-clipboard-manager';
import { QuickCaptureAttachmentPreview, type QuickCaptureAttachment, type AttachmentOrigin, type AttachmentMotion } from './QuickCaptureAttachmentPreview';
import './QuickCapture.css';
import { QuickCaptureHistory } from './QuickCaptureHistory';
import { createQuickCaptureBridge, EMPTY_HOST_STATE, QUICK_CAPTURE_READ, type QuickCaptureReadReceipt, type QuickCaptureClient, type QuickCaptureHostState, type QuickCaptureItem, type QuickCaptureDraft } from '../quickCapture/client';
import { QuickCaptureWorkQueue } from '../quickCapture/workQueue';
import { useQuickCaptureHistory } from '../quickCapture/useQuickCaptureHistory';
import { quickCaptureDraftCache, newQuickCaptureDraft } from '../quickCapture/draft';
import { useShortcut } from '../shortcuts/useShortcut';
import { parseKeybindingsConfig, setShortcutOverrides } from '../shortcuts/resolver';

const automationEnabled = (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
type Attachment = QuickCaptureAttachment;



type QuickCaptureProps = { client?: QuickCaptureClient; hostState?: QuickCaptureHostState; workQueue?: QuickCaptureWorkQueue };
export function QuickCapture({ client: suppliedClient, hostState, workQueue }: QuickCaptureProps = {}) {
  const [host, setHost] = useState(hostState ?? EMPTY_HOST_STATE);
  const [bridge, setBridge] = useState<ReturnType<typeof createQuickCaptureBridge>>();
  useEffect(() => {
    if (suppliedClient) return;
    const connection = createQuickCaptureBridge(setHost);
    setBridge(connection);
    return () => connection.dispose();
  }, [suppliedClient]);
  useEffect(() => { if (hostState) setHost(hostState); }, [hostState]);
  const client = useMemo(() => suppliedClient ?? bridge?.forProfile(host.profileId), [suppliedClient, bridge, host.profileId]);
  if (!client || !host.profileId) return <main className="capture" aria-busy="true">Connecting Quick Capture…</main>;
  return <QuickCaptureForProfile key={host.profileId} client={client} hostState={host} workQueue={workQueue} refresh={bridge?.refresh} />;
}

function QuickCaptureForProfile({ client: suppliedClient, hostState: host, workQueue: suppliedQueue, refresh }: Required<Pick<QuickCaptureProps, 'client' | 'hostState'>> & Pick<QuickCaptureProps, 'workQueue'> & { refresh?: () => Promise<void> }) {
  const client = useRef(suppliedClient);
  const [draftCache] = useState(() => quickCaptureDraftCache(host.profileId));
  const [workQueue] = useState(() => suppliedQueue ?? new QuickCaptureWorkQueue());
  const cache = useRef(draftCache);
  const [initialDraft] = useState(newQuickCaptureDraft);
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
  const [saved, setSaved] = useState<QuickCaptureItem[]>([]);
  const roster = host.mailboxes;
  const label = (id: string) => roster.find(item => item.id === id)?.name ?? id;
  const [text, setText] = useState('');
  const [mailbox, setMailbox] = useState('chief');
  const [files, setFiles] = useState<Attachment[]>([]);
  const [error, setError] = useState('');
  const { history, nextCursor, loading: loadingRecent, error: historyError, refresh: refreshRecent, markRead } = useQuickCaptureHistory(client.current, recent, host.connected);
  useEffect(() => {
    const listener = listen<QuickCaptureReadReceipt>(QUICK_CAPTURE_READ, ({ payload }) => {
      if (payload.profileId === host.profileId) markRead(payload);
    });
    return () => { void listener.then(unlisten => unlisten()); };
  }, [host.profileId]);
  const binding = host.binding ?? '';
  const [dragging, setDragging] = useState(false);
  const [picker, setPicker] = useState(false);
  const [pick, setPick] = useState(0);
  const mailboxMenu = useRef<HTMLDivElement>(null);
  const motion = useRef<(AttachmentMotion | { kind: 'drop'; phase: 'start' | 'end'; at: number })[]>([]);
  const mounted = useRef(true);
  const visible = useRef(false);
  const viewRecent = useRef(recent);
  useLayoutEffect(() => { viewRecent.current = recent; }, [recent]);
  const draftGeneration = useRef(0);
  const ownedFiles = useRef(new Set<string>());
  const stagedFiles = useRef(new Map<string, Promise<void>>());
  const onMotion = useCallback((receipt: AttachmentMotion) => { if (automationEnabled) motion.current.push(receipt); }, []);
  const onSettled = useCallback((id: string) => { setFiles(previous => previous.map(file => file.id === id ? { ...file, arriving: false } : file)); }, []);
  function settleEntrances() { setFiles(previous => previous.map(file => ({ ...file, arriving: false }))); }
  const latency = useRef<{ openedAt: number; focusedAt: number; nativeShowToFocusMs: number }[]>([]);
  const editor = useRef<HTMLTextAreaElement>(null);
  const historyView = useRef<HTMLElement>(null);
  const messageSelection = useRef<{ start: number; end: number; direction: 'forward' | 'backward' | 'none' }>(null);
  const composing = useRef(false);
  const sending = useRef(false);
  const state = useRef({ text, mailbox, files, saved, binding, uncertain, restored });
  useLayoutEffect(() => { state.current = { text, mailbox, files, saved, binding, uncertain, restored }; }, [text, mailbox, files, saved, binding, uncertain, restored]);

  function addAttachments(items: { name: string; load: () => Promise<string> }[], origin: AttachmentOrigin, silent = false) {
    if (!state.current.restored) { setError('Draft is still loading. Try adding the file again when it is ready.'); return; }
    setSaved([]);
    const generation = draftGeneration.current;
    const arrivals = items.map(item => ({ id: crypto.randomUUID(), name: item.name, url: '', origin, ready: false,
      arriving: visible.current && !viewRecent.current && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches }));
    arrivals.forEach(file => ownedFiles.current.add(file.id));
    setFiles(previous => [...previous, ...arrivals]);
    arrivals.forEach((file, index) => {
      void workQueue.run(async () => {
        if (draftGeneration.current !== generation || !ownedFiles.current.has(file.id)) return;
        const url = await items[index].load();
        if (draftGeneration.current !== generation || !ownedFiles.current.has(file.id)) return;
        setFiles(previous => previous.map(item => item.id === file.id ? { ...item, url } : item));
        const imagePreview = url.startsWith('data:image/') && await ImagePromise(url).then(() => true, () => false);
        if (draftGeneration.current !== generation || !ownedFiles.current.has(file.id)) return;
        setFiles(previous => previous.map(item => item.id === file.id ? { ...item, url, ready: true, imagePreview,
          arriving: item.arriving && visible.current && !viewRecent.current } : item));
      }).catch(error => {
        if (draftGeneration.current !== generation || !ownedFiles.current.delete(file.id)) return;
        setFiles(previous => previous.filter(item => item.id !== file.id));
        if (!silent) setError(`Cannot attach ${file.name}: ${error}`);
      });
    });
  }
  function addFiles(files: File[]) {
    const items = files.map(file => ({ name: file.name, load: () => fileDataUrl(file) }));
    addAttachments(items, { kind: 'paste' });
  }
  const removeFile = useCallback((id: string) => {
    if (sending.current || state.current.uncertain) return;
    const retained = draft();
    ownedFiles.current.delete(id); stagedFiles.current.delete(id);
    const file = state.current.files.find(item => item.id === id);
    setFiles(previous => previous.filter(item => item.id !== id));
    if (file?.url.startsWith('blob:')) URL.revokeObjectURL(file.url);
    void cache.current.save({ ...retained, files: retained.files.filter(file => file.id !== id) })
      .then(() => client.current!.discard(retained.id, [id]))
      .catch(error => setError(`Cannot remove file: ${error}`));
    editor.current?.focus();
  }, []);
  function pasteNativeImage() {
    addAttachments([{ name: 'Pasted screenshot', load: async () => {
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
  function draft(isUncertain = uncertain): QuickCaptureDraft {
    return { id: identity.current.id, text: state.current.text, mailbox: state.current.mailbox,
      files: state.current.files.filter(file => file.url).map(({ id, name, url }) => ({ id, name, url })), uncertain: isUncertain };
  }
  function stageDraft(staged: QuickCaptureDraft): Promise<void> {
    const fresh = staged.files.filter(file => !stagedFiles.current.has(file.id));
    for (const file of fresh) {
      const pending = workQueue.run(async () => {
        if (!mounted.current || identity.current.id !== staged.id || !ownedFiles.current.has(file.id)) return;
        await cache.current.save(draft());
        if (!mounted.current || identity.current.id !== staged.id || !ownedFiles.current.has(file.id)) return;
        await client.current!.stage({ ...staged, files: [file] });
      });
      stagedFiles.current.set(file.id, pending);
      void pending.catch(() => {
        if (stagedFiles.current.get(file.id) === pending) stagedFiles.current.delete(file.id);
      });
    }
    return Promise.all(staged.files.map(file => stagedFiles.current.get(file.id))).then(() => {});
  }
  async function send() {
    if (sending.current || !restored || files.some(file => !file.ready) || (!text.trim() && files.length === 0)) return;
    sending.current = true; setSubmitting(true); settleEntrances();
    try {
      let accepted: QuickCaptureItem | null = null;
      if (uncertain) {
        accepted = await client.current!.resolve(identity.current.id);
        if (!mounted.current) return;
        if (!accepted) setUncertain(false);
      }
      if (!accepted) {
        const outgoing = draft(false);
        await cache.current.save(outgoing);
        if (!mounted.current) return;
        if (outgoing.files.length) await stageDraft(outgoing);
        if (!mounted.current) return;
        const pending = { ...outgoing, uncertain: true };
        await cache.current.save(pending);
        if (!mounted.current) return;
        setUncertain(true);
        accepted = await client.current!.submit({ id: pending.id, text: pending.text, mailbox: pending.mailbox, fileIds: pending.files.map(file => file.id) });
      }
      await accept(accepted, true);
    } catch (error) { setError(String(error)); }
    finally { sending.current = false; setSubmitting(false); }
  }
  async function accept(accepted: QuickCaptureItem, hide: boolean) {
    if (!mounted.current) return;
    const next = newQuickCaptureDraft(); await cache.current.save(next); identity.current = next;
    draftGeneration.current++; ownedFiles.current.clear(); stagedFiles.current.clear();
    setSaved([accepted]);
    void refreshRecent();
    setText(''); setFiles([]); setMailbox('chief'); setUncertain(false); setError('');
    if (hide && mounted.current) await invoke('capture_hide');
  }
  async function resolveSubmission() {
    if (sending.current || !state.current.uncertain) return;
    sending.current = true; setResolving(true);
    try {
      const accepted = await client.current!.resolve(identity.current.id);
      if (!mounted.current) return;
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
      await cache.current.save(retained); identity.current = retained; stagedFiles.current.clear();
      if (!mounted.current) return;
      await client.current!.discard(previousId, files.map(file => file.id));
      if (!mounted.current) return;
      const next = newQuickCaptureDraft(); await cache.current.save(next); identity.current = next;
      draftGeneration.current++; ownedFiles.current.clear(); stagedFiles.current.clear(); setFiles([]); setText(''); setMailbox('chief');
    } catch (error) { stagedFiles.current.clear(); setError(String(error)); }
    finally { sending.current = false; setSubmitting(false); }
  }

  useEffect(() => {
    let disposed = false;
    void cache.current.read().then(stored => {
      if (disposed) return;
      if (stored) {
        identity.current = stored; setText(stored.text); setMailbox(stored.mailbox); setUncertain(stored.uncertain);
        setFiles(stored.files.map(file => ({ ...file, ready: true, arriving: false, origin: { kind: 'paste' } })));
        stored.files.forEach(file => ownedFiles.current.add(file.id));
      }
      setRestored(true);
    }, error => { if (!disposed) setError(`Cannot restore draft: ${error}`); });
    return () => { disposed = true; };
  }, [suppliedClient]);
  useEffect(() => { if (restored && uncertain && !submitting && host.connected) void resolveSubmission(); }, [restored, uncertain, submitting, host.connected]);
  useEffect(() => {
    if (!restored || !host.connected || uncertain || submitting || !files.length || files.some(file => !file.ready)) return;
    void stageDraft(draft()).catch(error => setError(`File upload: ${error}`));
  }, [files, restored, host.connected, uncertain, submitting]);
  useEffect(() => {
    if (!restored || sending.current) return;
    void cache.current.save(draft()).catch(error => setError(`Draft could not be saved: ${error}`));
  }, [text, mailbox, files, uncertain, restored]);

  useEffect(() => {
    if (!automationEnabled) return;
    let disposed = false;
    let cleanup: (() => void) | undefined;
    void import('../quickCapture/automationBridge').then(({ installQuickCaptureAutomationBridge }) => {
      if (!disposed) cleanup = installQuickCaptureAutomationBridge({ editor, state, composing, motion, latency, client, stageDraft: () => stageDraft(draft()) });
    });
    return () => { disposed = true; cleanup?.(); };
  }, []);

  useEffect(() => {
    mounted.current = true;
    hideBootSplash();
    document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "false";
    void getCurrentWebviewWindow().isVisible().then(isVisible => {
      if (!mounted.current) return;
      visible.current = isVisible;
      document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = String(isVisible);
    });
    editor.current?.focus();
    const open = listen<number>('capture-open', ({ payload }) => { visible.current = true; document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "true"; setRecent(false); setPicker(false); void refresh?.(); void resolveSubmission(); editor.current?.focus(); if (automationEnabled) latency.current.push({ openedAt: payload, focusedAt: Date.now(), nativeShowToFocusMs: Date.now() - payload }); });
    const hidden = listen("capture-hidden", () => { visible.current = false; setRecent(false); settleEntrances(); document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible = "false"; });
    const drop = getCurrentWebviewWindow().onDragDropEvent(async event => {
      setDragging(event.payload.type === 'enter' || event.payload.type === 'over');
      if (event.payload.type !== 'drop') return;
      if (sending.current || state.current.uncertain) return;
      const origin: AttachmentOrigin = { kind: 'drop', x: event.payload.position.x, y: event.payload.position.y };
      addAttachments(event.payload.paths.map(path => ({ name: path.split('/').pop() || 'Dropped file', load: () => invoke<string>('quick_capture_file_read', { path })
      })), origin);
    });
    return () => { mounted.current = false; draftGeneration.current++; ownedFiles.current.clear(); stagedFiles.current.clear(); state.current.files.forEach(file => { if (file.url.startsWith('blob:')) URL.revokeObjectURL(file.url); }); void open.then(f => f()); void hidden.then(f => f()); void drop.then(f => f()); };
  }, []);

  useEffect(() => {
    if (picker) mailboxMenu.current?.focus();
    else if (recent) historyView.current?.focus();
    else if (editor.current) {
      editor.current.focus();
      if (messageSelection.current) {
        const { start, end, direction } = messageSelection.current;
        editor.current.setSelectionRange(start, end, direction);
        messageSelection.current = null;
      }
    }
  }, [picker, recent]);

  function openPicker() { if (uncertain || submitting) return; setPick(Math.max(0, roster.findIndex(item => item.id === mailbox))); setRecent(false); setPicker(!picker); }
  const receipt = submitting ? 'Saving…' : resolving ? 'Checking submission…' : uncertain ? 'Submission unconfirmed' : !host.connected ? 'Connecting…' : saved.length ? `Saved for ${label(saved[saved.length - 1].mailbox)}` : '';
  return <main className="capture" data-mailbox={automationEnabled ? mailbox : undefined} data-font-scale={automationEnabled ? fontScale : undefined} style={{ '--ui-scale': fontScale } as CSSProperties} onKeyDown={event => {
    if (event.nativeEvent.isComposing || composing.current || event.keyCode === 229) return;
    if (event.metaKey && !event.altKey && !event.ctrlKey && !event.shiftKey && /^[1-4]$/.test(event.key)) {
      event.preventDefault(); if (uncertain || submitting) return; if (roster[Number(event.key) - 1]) setMailbox(roster[Number(event.key) - 1].id); setPicker(false);
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
    if (picker && !(event.target as Element).closest('.capture-menu, .capture-mailbox')) setPicker(false);
  }}>
    {!recent && <header className="capture-top" data-tauri-drag-region>
      <button aria-label="Recent messages" className="capture-recent-toggle" aria-pressed={recent} onClick={() => {
        if (!recent && editor.current) messageSelection.current = { start: editor.current.selectionStart, end: editor.current.selectionEnd, direction: editor.current.selectionDirection };
        settleEntrances(); setRecent(!recent); setPicker(false);
      }}>Recent</button>
      <button aria-label="Send to" disabled={uncertain || submitting} title="Send to · ⌘K" className="capture-mailbox" aria-haspopup="listbox" aria-expanded={picker} onClick={openPicker}>To: {label(mailbox)} <ChevronIcon /></button>
    </header>}
    {recent ? <QuickCaptureHistory regionRef={historyView} client={client.current!} history={history}
      hasDraft={!!text || files.length > 0} submitting={submitting}
      loading={loadingRecent} nextCursor={nextCursor} label={label} onRefresh={refreshRecent}
      onBack={() => setRecent(false)} onDiscard={() => void discardDraft()} /> : <>
      <textarea autoFocus ref={editor} aria-label="Message" placeholder={`Message ${label(mailbox)}`} value={text} readOnly={uncertain || submitting || !restored} onChange={event => { setSaved([]); setText(event.target.value); }} onCompositionStart={event => { composing.current = true; event.currentTarget.dataset.composing = "true"; }} onCompositionEnd={event => { composing.current = false; event.currentTarget.dataset.composing = "false"; }} onKeyDown={event => {
        if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && !composing.current && event.keyCode !== 229) { event.preventDefault(); void send(); }
      }} onPaste={event => {
        if (uncertain || sending.current) { event.preventDefault(); return; }
        const files = [...event.clipboardData.files];
        if (files.length) { event.preventDefault(); addFiles(files); }
        else void pasteNativeImage();
      }} />
      {files.length > 0 && <div className="capture-files">{files.map(file => <QuickCaptureAttachmentPreview key={file.id} file={file} onSettled={onSettled} onMotion={onMotion} onRemove={removeFile} disabled={uncertain || submitting} />)}</div>}
    </>}
    {(host.connectionError || error || historyError || host.shortcutError) && <p role="alert" className="capture-error">{host.connectionError || error || historyError || host.shortcutError}</p>}
    {!recent && <footer className="capture-footer">
      {receipt && <span className="capture-receipt" aria-live="polite"><i />{receipt}</span>}
      <span className="capture-keys"><kbd>⇧↵</kbd> new line</span>
      <button className="send-button" disabled={!restored || submitting || resolving || files.some(file => !file.ready) || (!text.trim() && !files.length)} onClick={() => void send()}>{uncertain ? 'Retry' : 'Send'} <kbd>↵</kbd></button>
    </footer>}
    {picker && <div ref={mailboxMenu} className="capture-menu" role="listbox" aria-label="Send to" tabIndex={-1} aria-activedescendant={`mailbox-${pick}`} onKeyDown={event => {
      if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
        event.preventDefault(); event.stopPropagation();
        if (event.key === 'ArrowDown') setPick((pick + 1) % roster.length);
        else if (event.key === 'ArrowUp') setPick((pick + roster.length - 1) % roster.length);
        else if (event.key === 'Home') setPick(0);
        else if (event.key === 'End') setPick(roster.length - 1);
        else { setMailbox(roster[pick].id); setPicker(false); }
      } else if (event.key === 'Tab') setPicker(false);
    }}>{roster.map((item, index) => <div id={`mailbox-${index}`} role="option" aria-selected={index === pick} key={item.id} className={item.id === mailbox ? 'chosen' : ''} onPointerMove={() => setPick(index)} onClick={() => { setMailbox(item.id); setPicker(false); }}>
      {item.name} <span>{item.detail}</span>{index < 4 && <kbd>⌘{index + 1}</kbd>}
    </div>)}</div>}
    {dragging && <div className="capture-drop" onAnimationStart={() => { if (automationEnabled) motion.current.push({ kind: 'drop', phase: 'start', at: Date.now() }); }} onAnimationEnd={() => { if (automationEnabled) motion.current.push({ kind: 'drop', phase: 'end', at: Date.now() }); }}>Drop to attach to this message</div>}
  </main>;
}

function ChevronIcon() { return <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M2 3.5l3 3 3-3" /></svg>; }


function ImagePromise(url: string): Promise<void> {
  return new Promise((resolve, reject) => { const image = new Image(); image.onload = () => resolve(); image.onerror = reject; image.src = url; });
}


function fileDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader(); reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error); reader.readAsDataURL(blob);
  });
}
