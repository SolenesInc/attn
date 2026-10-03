import { useEffect, useRef, useState, type Ref } from 'react';
import type { CaptureClient, CaptureItem } from '../quickCapture/client';

interface Props {
  regionRef: Ref<HTMLElement>; client: CaptureClient; history: CaptureItem[];
  hasDraft: boolean; submitting: boolean; loading: boolean; nextCursor?: string;
  label: (id: string) => string; onRefresh: (cursor?: string) => Promise<void>;
  onBack: () => void; onDiscard: () => void;
}

export function QuickCaptureHistory({ regionRef, client, history, hasDraft, submitting,
  loading, nextCursor, label, onRefresh, onBack, onDiscard }: Props) {
  return <section ref={regionRef} tabIndex={-1} className="capture-history" aria-label="Recent captures">
    <div className="capture-history-heading" data-tauri-drag-region>
      <h1 data-tauri-drag-region>Recent captures</h1>
      <button className="capture-history-back" onClick={onBack}>Back to note <kbd>esc</kbd></button>
    </div>
    {hasDraft && <div className="capture-history-draft"><span>Current draft</span><button disabled={submitting} onClick={onDiscard}>Discard draft</button></div>}
    {!history.length && <p className="capture-history-empty">No saved captures yet.</p>}
    <div className="capture-history-list">{history.map(item => <article key={item.id} className="capture-history-item">
      <header><strong>{label(item.recipient)}</strong><span title={item.readAt ? 'Retrieved from the inbox; not necessarily understood or acted upon.' : undefined}>
        {item.readAt ? 'Read' : 'Sent'} · <time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time>
      </span></header>
      {item.text && <p className="capture-history-note">{item.text}</p>}
      {item.images.length > 0 && <div className="capture-history-images">{item.images.map(file => file.mediaType?.startsWith('image/')
        ? <CaptureHistoryImage key={file.id} client={client} captureId={item.id} image={file} />
        : <span key={file.id} className="capture-history-image" title={file.name}>{file.name}</span>)}</div>}
    </article>)}</div>
    {nextCursor && <button className="capture-history-older" disabled={loading} onClick={() => void onRefresh(nextCursor)}>Show older captures</button>}
  </section>;
}

const imageLoads = new WeakMap<CaptureClient, Promise<void>>();

function CaptureHistoryImage({ client, captureId, image }: { client: CaptureClient; captureId: string; image: { id: string; name: string; mediaType?: string } }) {
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
        const value = await client.image(captureId, image.id, image.mediaType);
        if (disposed) release(value); else { owned = value; setUrl(value); }
      } catch (error) { if (!disposed) setError(String(error)); }
    });
    imageLoads.set(client, load);
    return () => { disposed = true; release(owned); owned = ''; };
  }, [visible, client, captureId, image.id, image.mediaType, retry]);
  return <figure ref={figure} className="capture-history-image"><div className="capture-history-image-preview">{url && <img src={url} alt={image.name} />}</div><figcaption>{image.name}{error && <><span role="alert"> {error}</span><button aria-label={`Retry ${image.name}`} onClick={() => setRetry(value => value + 1)}>Retry image</button></>}</figcaption></figure>;
}
