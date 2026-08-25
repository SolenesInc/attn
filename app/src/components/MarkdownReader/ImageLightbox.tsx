import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../hooks/useEscapeStack';

export interface ImageLightboxProps {
  src: string;
  alt: string;
  onClose: () => void;
}

// Portals to document.body so it escapes tile overflow/stacking contexts. Escape goes
// through useEscapeStack, so one press closes only the lightbox, not the tile underneath.
export function ImageLightbox({ src, alt, onClose }: ImageLightboxProps) {
  useEscapeStack(onClose, true);

  return createPortal(
    <div className="md-lightbox" role="dialog" aria-modal="true" onClick={onClose}>
      <img
        className="md-lightbox-img"
        src={src}
        alt={alt}
        onClick={(event) => event.stopPropagation()}
      />
      {alt && <div className="md-lightbox-caption">{alt}</div>}
    </div>,
    document.body,
  );
}
