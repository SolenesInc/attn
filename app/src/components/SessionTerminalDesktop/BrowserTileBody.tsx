import { useEffect, useRef, useState } from 'react';
import {
  browserHostLabel,
  mountBrowserHost,
  unmountBrowserHost,
  updateBrowserHost,
  type BrowserHostRect,
} from '../../browser/host';
import { useDesktopShelfStore } from '../../store/desktopShelf';
import { useSidebarDrag } from '../../store/sidebarDrag';
import { nativeWindowLabel, useTileWindow } from '../../tileWindow';
import { SHORTCUTS, type ShortcutId } from '../../shortcuts/registry';
import { dispatchShortcut } from '../../shortcuts/useShortcut';

function hostRect(element: HTMLElement): BrowserHostRect {
  const rect = element.getBoundingClientRect();
  return {
    x: rect.left,
    y: rect.top,
    width: rect.width,
    height: rect.height,
  };
}

export function BrowserTileBody({
  desktopId,
  tileId,
  url,
  dragging: tileDragging,
  visible: visibleInTile,
  paneGeometry,
  onClose,
}: {
  desktopId: string;
  tileId: string;
  url: string;
  dragging: boolean;
  visible: boolean;
  paneGeometry?: string;
  onClose: () => void;
}) {
  const tileWindow = useTileWindow();
  const inMainWindow = tileWindow === window;
  const peekOverDesktop = useDesktopShelfStore((state) => state.peek?.desktopId === desktopId);
  const visible = visibleInTile && !(inMainWindow && peekOverDesktop);
  const sidebarDragging = useSidebarDrag((state) => state.dragging);
  const dragging = tileDragging || (inMainWindow && sidebarDragging);
  const hostRef = useRef<HTMLDivElement>(null);
  const tileWindowRef = useRef(tileWindow);
  tileWindowRef.current = tileWindow;
  const draggingRef = useRef(dragging);
  const visibleRef = useRef(visible);
  const nativeLocationsRef = useRef<string[]>([]);
  const lifecycleQueueRef = useRef<Promise<void>>(Promise.resolve());
  const mountGenerationRef = useRef(0);
  const mountedRef = useRef(true);
  const [error, setError] = useState<string | null>(null);
  const label = browserHostLabel(desktopId, tileId);
  draggingRef.current = dragging;
  visibleRef.current = visible;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      mountGenerationRef.current += 1;
      const unmount = lifecycleQueueRef.current
        .catch(() => {})
        .then(() => unmountBrowserHost(label));
      lifecycleQueueRef.current = unmount.catch((unmountError) => {
        console.warn('[BrowserTile] Failed to unmount browser host:', unmountError);
      });
    };
  }, [label]);

  useEffect(() => {
    const handleLocation = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail;
      if (
        typeof detail === 'object'
        && detail !== null
        && 'label' in detail
        && 'url' in detail
        && detail.label === label
        && typeof detail.url === 'string'
        && detail.url !== url
      ) {
        nativeLocationsRef.current = [
          ...nativeLocationsRef.current.filter((url) => url !== detail.url),
          detail.url,
        ].slice(-16);
      }
    };
    window.addEventListener('attn:browser-location', handleLocation);
    return () => window.removeEventListener('attn:browser-location', handleLocation);
  }, [label, url]);

  useEffect(() => {
    const element = hostRef.current;
    if (!element || !url) return;
    const nativeLocationIndex = nativeLocationsRef.current.indexOf(url);
    if (nativeLocationIndex >= 0) {
      nativeLocationsRef.current.splice(nativeLocationIndex, 1);
      return;
    }
    const generation = ++mountGenerationRef.current;
    const mount = lifecycleQueueRef.current
      .catch(() => {})
      .then(async () => {
        if (!mountedRef.current || generation !== mountGenerationRef.current) return;
        await mountBrowserHost(
          label,
          url,
          nativeWindowLabel(tileWindowRef.current),
          hostRect(element),
          visibleRef.current && !draggingRef.current,
        );
        if (!mountedRef.current) return;
        const currentElement = hostRef.current;
        if (currentElement) {
          await updateBrowserHost(
            label,
            nativeWindowLabel(tileWindowRef.current),
            hostRect(currentElement),
            visibleRef.current && !draggingRef.current,
          );
        }
        if (generation === mountGenerationRef.current) setError(null);
      });
    lifecycleQueueRef.current = mount.catch((mountError) => {
      if (mountedRef.current && generation === mountGenerationRef.current) {
        setError(String(mountError));
      }
    });
  }, [label, url]);

  useEffect(() => {
    const element = hostRef.current;
    if (!element) return;
    const sync = () => {
      void updateBrowserHost(label, nativeWindowLabel(tileWindow), hostRect(element), visible && !dragging).catch((updateError) => {
        if (!String(updateError).includes('not mounted')) {
          console.warn('[BrowserTile] Failed to update browser host:', updateError);
        }
      });
    };
    const observer = new tileWindow.ResizeObserver(sync);
    observer.observe(element);
    tileWindow.addEventListener('resize', sync);
    sync();
    return () => {
      observer.disconnect();
      tileWindow.removeEventListener('resize', sync);
    };
  }, [dragging, label, paneGeometry, tileWindow, visible]);

  useEffect(() => {
    const handleNativeClose = (event: Event) => {
      if ((event as CustomEvent<unknown>).detail === label) {
        onClose();
      }
    };
    window.addEventListener('attn:native-browser-close', handleNativeClose);
    return () => {
      window.removeEventListener('attn:native-browser-close', handleNativeClose);
    };
  }, [label, onClose]);

  useEffect(() => {
    const handleShortcut = (event: Event) => {
      const detail = (event as CustomEvent<{ label?: unknown; id?: unknown }>).detail;
      if (detail?.label !== label || typeof detail.id !== 'string'
        || !Object.prototype.hasOwnProperty.call(SHORTCUTS, detail.id)) return;
      const id = detail.id as ShortcutId;
      const source = inMainWindow && id !== 'tile.popOut' ? undefined : { desktopId, leafId: tileId };
      dispatchShortcut(id, source, tileWindow);
    };
    window.addEventListener('attn:native-browser-shortcut', handleShortcut);
    return () => window.removeEventListener('attn:native-browser-shortcut', handleShortcut);
  }, [desktopId, inMainWindow, label, tileId, tileWindow]);

  return (
    <div className="browser-tile-host" ref={hostRef}>
      {error ? <div className="desktop-dock-tile-message desktop-dock-tile-error">{error}</div> : null}
    </div>
  );
}
