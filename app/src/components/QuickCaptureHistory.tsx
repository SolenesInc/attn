import { useEffect, useRef, useState, type Ref } from 'react';
import type { CaptureClient, CaptureDraftAsset, CaptureItem, CaptureRecipient } from '../quickCapture/client';

interface Props {
  regionRef: Ref<HTMLElement>; client: CaptureClient; history: CaptureItem[]; assets: CaptureDraftAsset[];
  roster: CaptureRecipient[]; currentDraftId: string; hasDraft: boolean; submitting: boolean;
  loading: boolean; nextCursor?: string; label: (id: string) => string;
  onRefresh: (cursor?: string) => Promise<void>; onError: (message: string) => void;
  onBack: () => void; onDiscard: () => void; onFollowUp: (item: CaptureItem) => void;
}

export function QuickCaptureHistory({ regionRef, client, history, assets, roster, currentDraftId, hasDraft, submitting,
  loading, nextCursor, label, onRefresh, onError, onBack, onDiscard, onFollowUp }: Props) {
  const update = (item: CaptureItem, action: 'cancel' | 'restore' | 'retry' | 'redirect', recipient?: string) => {
    void client.update(item.id, action, recipient).then(() => onRefresh()).catch(error => onError(String(error)));
  };
  return <section ref={regionRef} tabIndex={-1} className="capture-history" aria-label="Recent captures">
    <div className="capture-history-heading" data-tauri-drag-region>
      <h1 data-tauri-drag-region>Recent captures</h1>
      <button className="capture-history-back" onClick={onBack}>Back to note <kbd>esc</kbd></button>
    </div>
    {hasDraft && <div className="capture-history-draft"><span>Current draft</span><button disabled={submitting} onClick={onDiscard}>Discard draft</button></div>}
    {!history.length && <p className="capture-history-empty">No saved captures yet.</p>}
    <div className="capture-history-list">{history.map(item => <article key={item.id} className="capture-history-item">
      <header><strong>{label(item.recipient)}</strong><span className="capture-history-state" data-state={item.state}
        title={item.state === 'read' ? 'Retrieved from the inbox; not necessarily understood or acted upon.' : undefined}>{item.state.replace(/_/g, ' ')}</span></header>
      {item.text && <p className="capture-history-note">{item.text}</p>}
      {item.images.length > 0 && <div className="capture-history-images">{item.images.map(image => <CaptureHistoryImage key={image.id} client={client} captureId={item.id} image={image} />)}</div>}
      {item.detail && <p className="capture-history-detail" role="status">{item.detail}</p>}
      <div className="capture-history-actions">{item.state === 'read' ? <>
        {item.sessionId && <button onClick={() => void client.openRecipient(item.sessionId!).catch(error => onError(String(error)))}>Open recipient</button>}
        <button onClick={() => onFollowUp(item)}>Follow up</button>
      </> : <>
        <button onClick={() => update(item, item.state === 'cancelled' ? 'restore' : 'cancel')}>{item.state === 'cancelled' ? 'Restore' : 'Cancel'}</button>
        {item.state === 'needs_attention' && <button className="capture-history-retry" onClick={() => update(item, 'retry')}>Retry delivery</button>}
        {item.state !== 'cancelled' && <select aria-label="Redirect capture" value="" onChange={event => update(item, 'redirect', event.target.value)}><option value="" disabled>Redirect to…</option>{roster.map(recipient => <option key={recipient.id} value={recipient.id}>{recipient.name}</option>)}</select>}
      </>}</div>
    </article>)}</div>
    {nextCursor && <button className="capture-history-older" disabled={loading} onClick={() => void onRefresh(nextCursor)}>Show older captures</button>}
    {assets.length > 0 && <aside className="capture-history-uploads" aria-label="Unsent image uploads"><h2>Unsent image uploads</h2>{assets.map(asset => <div className="capture-history-upload" key={`${asset.captureId}:${asset.id}`}>
      <span className="capture-history-upload-name">{asset.name}<small>{asset.captureId === currentDraftId ? 'In your current draft' : asset.state}</small></span>
      {asset.captureId !== currentDraftId && <button onClick={() => void client.discard(asset.captureId, [asset.id]).then(() => onRefresh()).catch(error => onError(String(error)))}>Discard upload</button>}
    </div>)}</aside>}
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
