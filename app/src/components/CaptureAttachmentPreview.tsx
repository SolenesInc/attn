import { useLayoutEffect, useRef, useState } from 'react';

export type AttachmentOrigin = { kind: 'drop'; x: number; y: number } | { kind: 'paste' };
export type CaptureAttachment = { id: string; name: string; url: string; ready: boolean; imagePreview?: boolean; arriving: boolean; origin: AttachmentOrigin };
export type AttachmentMotion = {
  kind: 'file'; phase: 'start' | 'end'; at: number; id: string;
  source?: { kind: 'drop' | 'paste'; x: number; y: number };
  target?: { x: number; y: number; width: number; height: number };
  duration?: number; cancelled?: boolean;
};

type Props = {
  file: CaptureAttachment;
  onSettled: (id: string) => void;
  onMotion: (receipt: AttachmentMotion) => void;
  onRemove: (id: string) => void;
  disabled?: boolean;
};

export function CaptureAttachmentPreview({ file, onSettled, onMotion, onRemove, disabled }: Props) {
  const slot = useRef<HTMLElement>(null);
  const [failed, setFailed] = useState(false);
  const isImage = !failed && (file.imagePreview ?? file.url.startsWith('data:image/'));
  useLayoutEffect(() => {
    if (!file.ready || !file.arriving) return;
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      onSettled(file.id);
      return;
    }
    const target = slot.current!.getBoundingClientRect();
    const editor = document.querySelector('.capture textarea')!.getBoundingClientRect();
    const scale = Math.min(2.3, editor.width / target.width / 2, editor.height / target.height);
    const center = { x: target.x + target.width / 2, y: target.y + target.height / 2 };
    const source = file.origin.kind === 'drop' ? file.origin : {
      kind: 'paste' as const, x: editor.x + editor.width / 2,
      y: editor.bottom - target.height * scale / 2,
    };
    const dx = source.x - center.x;
    const dy = source.y - center.y;
    const transform = (x: number, y: number, size: number, angle: number) => `translate(${x}px, ${y}px) scale(${size}) rotate(${angle}deg)`;
    const ghost = document.createElement('div');
    ghost.className = 'capture-flight';
    ghost.setAttribute('aria-hidden', 'true');
    Object.assign(ghost.style, {
      left: `${target.x}px`, top: `${target.y}px`, width: `${target.width}px`, height: `${target.height}px`,
    });
    const preview = document.createElement(isImage ? 'img' : 'span');
    if (preview instanceof HTMLImageElement) { preview.src = file.url; preview.alt = ''; }
    else { preview.textContent = file.name; Object.assign(preview.style, { display: 'grid', placeItems: 'center', height: '100%', padding: '8px', fontSize: '12px', color: '#fff', overflowWrap: 'anywhere' }); }
    ghost.append(preview);
    document.body.append(ghost);
    const paste = source.kind === 'paste';
    // A finite lift/flight places the file in its reserved slot.
    const duration = paste ? 560 : 460;
    const flight = ghost.animate([
      { transform: transform(dx, dy, paste ? scale * .82 : scale, paste ? -5 : -2), opacity: paste ? 0 : 1, offset: 0, easing: 'cubic-bezier(.2,.7,.3,1)' },
      { transform: transform(dx, dy - target.height / 8, scale, paste ? 2 : -3), opacity: 1, offset: paste ? .28 : .12, easing: 'cubic-bezier(.4,0,.2,1)' },
      { transform: transform(0, -target.height / 16, 1.06, 0), opacity: 1, offset: .86, easing: 'ease-out' },
      { transform: transform(0, 0, 1, 0), opacity: 1, offset: 1 },
    ], { duration, fill: 'both' });
    onMotion({ kind: 'file', phase: 'start', id: file.id, at: Date.now(), source, target: { ...center, width: target.width, height: target.height }, duration });
    let settled = false;
    function finish(cancelled: boolean) {
      if (settled) return;
      settled = true;
      flight.cancel();
      ghost.remove();
      onMotion({ kind: 'file', phase: 'end', id: file.id, at: Date.now(), cancelled });
      onSettled(file.id);
    }
    void flight.finished.then(() => finish(false), () => finish(true));
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
    const reduceMotion = () => { if (reduce.matches) finish(true); };
    reduce.addEventListener('change', reduceMotion);
    return () => { reduce.removeEventListener('change', reduceMotion); finish(true); };
  }, [file.id, file.url, file.ready, file.arriving, file.origin, isImage, onSettled, onMotion]);

  return <figure ref={slot} data-file-ready={file.ready} title={file.name} className={!file.ready || file.arriving ? 'arriving' : undefined}>
    {file.ready && (isImage ? <img src={file.url} alt={file.name} onError={() => setFailed(true)} /> : <figcaption style={{ padding: '8px', fontSize: '12px', overflowWrap: 'anywhere', opacity: file.arriving ? 0 : 1 }}>{file.name}</figcaption>)}
    <button disabled={disabled} aria-label={`Remove ${file.name}`} onClick={() => onRemove(file.id)}><svg width="9" height="9" viewBox="0 0 10 10" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6" /></svg></button>
  </figure>;
}
