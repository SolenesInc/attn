import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { EditorView } from '@codemirror/view';
import { isDomInstance } from '../utils/realm';
import { TileWindowProvider } from '.';
import { useTileWindowStore } from './pool';

function createHost(): HTMLDivElement {
  const host = document.createElement('div');
  host.className = 'tile-host';
  return host;
}

function focusedInside(host: HTMLElement): HTMLElement | null {
  const active = host.ownerDocument.activeElement;
  return isDomInstance(active, 'HTMLElement') && host.contains(active) ? active : null;
}

function rebindEditors(host: HTMLElement): void {
  for (const element of Array.from(host.querySelectorAll<HTMLElement>('.cm-editor'))) {
    EditorView.findFromDOM(element)?.setRoot(host.ownerDocument);
  }
}

export function MovablePane({ leafKey, popped, children }: { leafKey: string; popped: boolean; children: ReactNode }) {
  const [host] = useState(createHost);
  const slotRef = useRef<HTMLDivElement>(null);
  const tileWindow = useTileWindowStore((state) => (popped ? state.byLeaf.get(leafKey) : undefined));
  const [hostWindow, setHostWindow] = useState<Window>(window);

  useLayoutEffect(() => {
    const target = tileWindow?.mount ?? slotRef.current;
    if (!target) return;
    host.hidden = popped && !tileWindow;
    if (host.parentNode === target) return;
    const focused = focusedInside(host);
    target.appendChild(host);
    rebindEditors(host);
    setHostWindow(tileWindow?.window ?? window);
    const input = focused ?? (tileWindow ? host.querySelector<HTMLElement>('.terminal-container, .cm-content') : null);
    input?.focus({ preventScroll: true });
  }, [host, popped, tileWindow]);

  useEffect(() => () => host.remove(), [host]);

  return (
    <div ref={slotRef} className="tile-host-slot">
      {createPortal(<TileWindowProvider value={hostWindow}>{children}</TileWindowProvider>, host)}
    </div>
  );
}
