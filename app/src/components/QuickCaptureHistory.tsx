import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type Ref } from 'react';
import type { CaptureClient, CaptureItem } from '../quickCapture/client';

interface Props {
  regionRef: Ref<HTMLElement>; client: CaptureClient; history: CaptureItem[];
  hasDraft: boolean; submitting: boolean; loading: boolean; nextCursor?: string;
  label: (id: string) => string; onRefresh: (cursor?: string) => Promise<void>;
  onBack: () => void; onDiscard: () => void;
}

export function QuickCaptureHistory({ regionRef, client, history, hasDraft, submitting,
  loading, nextCursor, label, onRefresh, onBack, onDiscard }: Props) {
  const ordered = useMemo(() => [...history].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.id.localeCompare(b.id)), [history]);
  const [selected, setSelected] = useState<string>();
  const timeline = useRef<HTMLDivElement>(null);
  const rows = useRef(new Map<string, HTMLElement>());
  const populated = useRef(false);
  const anchor = useRef<{ id: string; offset: number } | null>(null);
  useLayoutEffect(() => {
    const scroll = timeline.current!;
    if (!ordered.length) { populated.current = false; setSelected(undefined); return; }
    if (!populated.current) {
      populated.current = true;
      setSelected(ordered[ordered.length - 1].id);
      scroll.scrollTop = scroll.scrollHeight;
    } else if (anchor.current && !loading) {
      const row = rows.current.get(anchor.current.id);
      if (row) scroll.scrollTop += row.getBoundingClientRect().top - scroll.getBoundingClientRect().top - anchor.current.offset;
      anchor.current = null;
    }
  }, [ordered, loading]);
  function loadOlder() {
    const scroll = timeline.current!;
    const top = scroll.getBoundingClientRect().top;
    const first = ordered.find(item => rows.current.get(item.id)!.getBoundingClientRect().bottom > top);
    if (first) anchor.current = { id: first.id, offset: rows.current.get(first.id)!.getBoundingClientRect().top - top };
    void onRefresh(nextCursor);
  }
  return <section ref={regionRef} tabIndex={-1} className="capture-history" aria-label="Recent captures" onKeyDown={event => {
    if (event.nativeEvent.isComposing || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onBack(); }
    else if ((event.key === 'ArrowUp' || event.key === 'ArrowDown') && ordered.length) {
      event.preventDefault(); event.stopPropagation();
      const current = Math.max(0, ordered.findIndex(item => item.id === selected));
      const index = Math.max(0, Math.min(ordered.length - 1, current + (event.key === 'ArrowUp' ? -1 : 1)));
      setSelected(ordered[index].id);
      rows.current.get(ordered[index].id)?.scrollIntoView({ block: 'nearest' });
    }
  }}>
    <div className="capture-history-heading" data-tauri-drag-region>
      <h1 data-tauri-drag-region>Recent</h1><span className="capture-history-count">{ordered.length} {ordered.length === 1 ? 'note' : 'notes'}</span>
      <button className="capture-history-back" onClick={onBack}>Back to note <kbd>esc</kbd></button>
    </div>
    {hasDraft && <div className="capture-history-draft"><span>Current draft</span><button disabled={submitting} onClick={onDiscard}>Discard draft</button></div>}
    <div ref={timeline} className="capture-history-list">
      {nextCursor && <button className="capture-history-older" disabled={loading} onClick={loadOlder}>Show older captures</button>}
      {!ordered.length && <p className="capture-history-empty">{loading ? 'Loading captures…' : 'No saved captures yet.'}</p>}
      <div className="capture-history-messages" role="list" aria-label="Sent messages">{ordered.map((item, index) => <Fragment key={item.id}>
        {(!index || dayKey(ordered[index - 1].createdAt) !== dayKey(item.createdAt)) && <div className="capture-history-day" role="presentation">{dayLabel(item.createdAt)}</div>}
        <article ref={node => { if (node) rows.current.set(item.id, node); else rows.current.delete(item.id); }} role="listitem" aria-current={item.id === selected ? 'true' : undefined} data-capture-id={item.id} className="capture-history-item" onClick={() => setSelected(item.id)}>
          <header className="capture-history-recipient"><span className="capture-history-avatar" aria-hidden="true" style={{ '--recipient-color': recipientColor(item.recipient) } as CSSProperties}>{label(item.recipient).slice(0, 1).toUpperCase()}</span><span>to {label(item.recipient)}</span></header>
          <div className="capture-history-bubble">
            {item.text && <p className="capture-history-note">{item.text}</p>}
            {item.files.length > 0 && <div className="capture-history-files">{item.files.map(file => file.mediaType?.startsWith('image/')
              ? <CaptureHistoryImage key={file.id} client={client} captureId={item.id} image={file} />
              : <div key={file.id} className="capture-history-file" title={file.name}><span className="capture-history-file-type">{fileType(file.name, file.mediaType)}</span><span className="capture-history-file-label"><span>{file.name}</span><small>{fileSize(file.bytes)}</small></span></div>)}</div>}
          </div>
          <div className="capture-history-status" data-read={!!item.readAt} title={item.readAt ? 'Retrieved from the inbox; not necessarily understood or acted upon.' : undefined}>
            <svg viewBox="0 0 12 12" aria-hidden="true">{item.readAt ? <path d="m2.5 6 2.25 2.25L9.5 3.5" /> : <circle cx="6" cy="6" r="3.5" />}</svg>
            <span>{item.readAt ? 'Read' : 'Sent'} <time dateTime={item.readAt ?? item.createdAt}>{new Date(item.readAt ?? item.createdAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</time></span>
          </div>
        </article>
      </Fragment>)}</div>
    </div>
    <footer className="capture-history-keys"><span><kbd>↑</kbd><kbd>↓</kbd> move</span><span><kbd>esc</kbd> back to note</span></footer>
  </section>;
}

function dayKey(date: string) { return new Date(date).toDateString(); }
function dayLabel(date: string) {
  const today = new Date(), yesterday = new Date(); yesterday.setDate(today.getDate() - 1);
  const day = new Date(date);
  if (day.toDateString() === today.toDateString()) return 'Today';
  if (day.toDateString() === yesterday.toDateString()) return 'Yesterday';
  return day.toLocaleDateString([], { month: 'short', day: 'numeric', ...(day.getFullYear() !== today.getFullYear() && { year: 'numeric' }) });
}
function recipientColor(id: string) {
  const hash = [...id].reduce((hash, character) => (hash * 31 + character.codePointAt(0)!) >>> 0, 0);
  return ['#6b5bd6', '#2f8a57', '#b0567a'][hash % 3];
}
function fileType(name: string, mediaType?: string) {
  if (name.includes('.')) return name.slice(name.lastIndexOf('.') + 1).toUpperCase();
  return mediaType === 'application/pdf' ? 'PDF' : mediaType === 'text/plain' ? 'TXT' : 'FILE';
}
function fileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  const unit = bytes < 1024 ** 2 ? 'KiB' : bytes < 1024 ** 3 ? 'MiB' : 'GiB';
  const divisor = unit === 'KiB' ? 1024 : unit === 'MiB' ? 1024 ** 2 : 1024 ** 3;
  return `${(bytes / divisor).toLocaleString([], { maximumFractionDigits: 1 })} ${unit}`;
}

const imageLoads = new WeakMap<CaptureClient, Promise<void>>();

function CaptureHistoryImage({ client, captureId, image }: { client: CaptureClient; captureId: string; image: { id: string; name: string; mediaType?: string; bytes: number } }) {
  const figure = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(false);
  const [url, setUrl] = useState('');
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const observer = new IntersectionObserver(entries => setVisible(entries[0].isIntersecting));
    observer.observe(figure.current!);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    setUrl(''); setError('');
    if (!visible) return;
    let disposed = false;
    let owned = '';
    const release = (value: string) => { if (value.startsWith('blob:')) URL.revokeObjectURL(value); };
    const load = (imageLoads.get(client) ?? Promise.resolve()).then(async () => {
      if (disposed) return;
      try {
        const value = await client.file(captureId, image.id, image.mediaType);
        if (disposed) release(value); else { owned = value; setUrl(value); }
      } catch (error) { if (!disposed) setError(String(error)); }
    });
    imageLoads.set(client, load);
    return () => { disposed = true; release(owned); owned = ''; };
  }, [visible, client, captureId, image.id, image.mediaType, retry]);
  return <figure ref={figure} className="capture-history-image"><div className="capture-history-image-preview">{url && <img src={url} alt={image.name} />}</div><figcaption><span>{image.name}</span><small>{fileSize(image.bytes)}</small>{error && <><span role="alert"> {error}</span><button aria-label={`Retry ${image.name}`} onClick={() => setRetry(value => value + 1)}>Retry image</button></>}</figcaption></figure>;
}
