import { invoke } from '@tauri-apps/api/core';
import { create } from 'zustand';
import type { TileWindowFrame } from '../types/generated';
import { listenForShortcuts, type ShortcutSource } from '../shortcuts/useShortcut';
import { listenForEscape } from '../hooks/useEscapeStack';
import { recordUiDiag } from '../utils/uiDiagnosticsLog';
import { nameNativeWindow } from '.';

const FRAME_SETTLE_MS = 400;

export interface OpenTileWindow {
  label: string;
  window: Window;
  mount: HTMLElement;
}

interface PooledWindow extends OpenTileWindow {
  leafKey: string | null;
  source: ShortcutSource | undefined;
  daemonFrame: TileWindowFrame | null;
  reportedFrame: TileWindowFrame | null;
  settleTimer: number | null;
}

interface TileWindowState {
  byLeaf: ReadonlyMap<string, OpenTileWindow>;
}

export const useTileWindowStore = create<TileWindowState>(() => ({ byLeaf: new Map() }));

const pool: PooledWindow[] = [];
let queue: Promise<void> = Promise.resolve();
let nextLabel = 1;

export function _resetTileWindowsForTest(): void {
  for (const entry of pool) {
    if (entry.settleTimer !== null) window.clearTimeout(entry.settleTimer);
    entry.window.close();
  }
  pool.length = 0;
  queue = Promise.resolve();
  nextLabel = 1;
}

export function tileWindowKey(desktopId: string, leafId: string): string {
  return `${desktopId}::${leafId}`;
}

function sameFrame(a: TileWindowFrame | null, b: TileWindowFrame | null): boolean {
  return a !== null && b !== null && a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}

function publish(): void {
  const byLeaf = new Map<string, OpenTileWindow>();
  for (const entry of pool) {
    if (entry.leafKey) byLeaf.set(entry.leafKey, { label: entry.label, window: entry.window, mount: entry.mount });
  }
  useTileWindowStore.setState({ byLeaf });
}

function mirrorLook(target: Document): void {
  const source = document;
  const copyRoot = () => {
    for (const attribute of Array.from(target.documentElement.attributes)) {
      if (!source.documentElement.hasAttribute(attribute.name)) target.documentElement.removeAttribute(attribute.name);
    }
    for (const attribute of Array.from(source.documentElement.attributes)) {
      target.documentElement.setAttribute(attribute.name, attribute.value);
    }
    target.body.className = source.body.className;
  };
  const copyHead = () => {
    for (const node of Array.from(target.head.querySelectorAll('[data-tile-window-style]'))) node.remove();
    for (const node of Array.from(source.head.querySelectorAll('style, link[rel="stylesheet"]'))) {
      const clone = target.importNode(node, true);
      clone.setAttribute('data-tile-window-style', '');
      target.head.appendChild(clone);
    }
  };
  copyRoot();
  copyHead();
  new MutationObserver(copyRoot).observe(source.documentElement, { attributes: true });
  new MutationObserver(copyRoot).observe(source.body, { attributes: true, attributeFilter: ['class'] });
  new MutationObserver(copyHead).observe(source.head, { childList: true, subtree: true, characterData: true });
}

function buildDocument(tileWindow: Window): HTMLElement {
  const target = tileWindow.document;
  const meta = target.createElement('meta');
  meta.setAttribute('charset', 'utf-8');
  target.head.appendChild(meta);
  mirrorLook(target);
  target.body.style.cssText = 'margin:0;height:100vh;overflow:hidden;background:var(--bg-base);';
  const desktop = target.createElement('div');
  desktop.className = 'session-terminal-desktop tile-window-desktop';
  desktop.style.cssText = 'position:absolute;inset:0;';
  const panes = target.createElement('div');
  panes.className = 'session-terminal-panes';
  panes.style.cssText = 'position:absolute;inset:0;';
  desktop.appendChild(panes);
  target.body.appendChild(desktop);
  return panes;
}

async function openWindow(frame: TileWindowFrame): Promise<PooledWindow> {
  const label = `tile-window-${nextLabel++}`;
  await invoke('tile_window_prepare', { label, frame });
  const tileWindow = window.open('about:blank', label, 'popup=yes');
  recordUiDiag({ kind: 'tile_window_open', label, opened: tileWindow !== null });
  if (!tileWindow) throw new Error(`tile window ${label} did not open`);
  nameNativeWindow(tileWindow, label);
  tileWindow.addEventListener('keydown', (event) => {
    if (!event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'w') return;
    event.preventDefault();
    event.stopImmediatePropagation();
    window.dispatchEvent(new CustomEvent('attn:tile-window-close', { detail: { label } }));
  }, true);
  listenForShortcuts(tileWindow, () => pool.find((candidate) => candidate.label === label)?.source);
  listenForEscape(tileWindow);
  const entry: PooledWindow = {
    label,
    window: tileWindow,
    mount: buildDocument(tileWindow),
    leafKey: null,
    source: undefined,
    daemonFrame: frame,
    reportedFrame: null,
    settleTimer: null,
  };
  pool.push(entry);
  return entry;
}

async function place(leafKey: string, source: ShortcutSource, frame: TileWindowFrame, title: string): Promise<void> {
  const held = pool.find((entry) => entry.leafKey === leafKey);
  if (held) {
    held.daemonFrame = frame;
    held.window.document.title = title;
    if (!sameFrame(held.reportedFrame, frame)) await invoke('tile_window_show', { label: held.label, frame });
    return;
  }
  const pooled = pool.find((entry) => entry.leafKey === null);
  const entry = pooled ?? await openWindow(frame);
  entry.leafKey = leafKey;
  entry.source = source;
  entry.daemonFrame = frame;
  entry.reportedFrame = null;
  entry.window.document.title = title;
  publish();
  if (pooled) await invoke('tile_window_show', { label: entry.label, frame });
}

function settle(entry: PooledWindow): void {
  if (entry.settleTimer !== null) window.clearTimeout(entry.settleTimer);
  entry.settleTimer = null;
}

function enqueue(work: () => Promise<void>): Promise<void> {
  const next = queue.catch(() => {}).then(work);
  queue = next;
  return next;
}

export function showTileWindow(leafKey: string, source: ShortcutSource, frame: TileWindowFrame, title: string): Promise<void> {
  return enqueue(() => place(leafKey, source, frame, title));
}

export function hideTileWindow(leafKey: string): Promise<void> {
  return enqueue(async () => {
    const entry = pool.find((candidate) => candidate.leafKey === leafKey);
    if (!entry) return;
    settle(entry);
    entry.leafKey = null;
    entry.source = undefined;
    entry.daemonFrame = null;
    entry.reportedFrame = null;
    publish();
    await invoke('tile_window_hide', { label: entry.label });
  });
}

export function heldLeafKeys(): string[] {
  return pool.flatMap((entry) => (entry.leafKey ? [entry.leafKey] : []));
}

export function describeTileWindows() {
  return pool.map((entry) => ({
    label: entry.label,
    leafKey: entry.leafKey,
    title: entry.window.document.title,
    visibility: entry.window.document.visibilityState,
    paneIds: Array.from(entry.mount.querySelectorAll('[data-pane-id]'), (pane) => pane.getAttribute('data-pane-id')),
    focusedPaneId: entry.window.document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null,
    focusedElement: `${entry.window.document.activeElement?.tagName ?? ''}.${entry.window.document.activeElement?.className ?? ''}`,
    innerSize: { width: entry.window.innerWidth, height: entry.window.innerHeight },
    daemonFrame: entry.daemonFrame,
    reportedFrame: entry.reportedFrame,
  }));
}

export function tileWindowByLabel(label: string): Window | null {
  return pool.find((entry) => entry.label === label)?.window ?? null;
}

export function raiseTileWindow(leafKey: string): void {
  const entry = pool.find((candidate) => candidate.leafKey === leafKey);
  if (entry) void invoke('tile_window_raise', { label: entry.label });
}

export interface TileWindowListeners {
  onFrameSettled: (leafKey: string, frame: TileWindowFrame) => void;
  onCloseRequested: (leafKey: string) => void;
}

export function listenToTileWindows({ onFrameSettled, onCloseRequested }: TileWindowListeners): () => void {
  const entryFor = (event: Event) => {
    const label = (event as CustomEvent<{ label?: unknown }>).detail?.label;
    return pool.find((entry) => entry.label === label && entry.leafKey !== null);
  };
  const handleFrame = (event: Event) => {
    const entry = entryFor(event);
    const frame = (event as CustomEvent<{ frame?: TileWindowFrame }>).detail?.frame;
    if (!entry || !frame) return;
    entry.reportedFrame = frame;
    settle(entry);
    entry.settleTimer = window.setTimeout(() => {
      entry.settleTimer = null;
      if (entry.leafKey && !sameFrame(entry.daemonFrame, frame)) {
        entry.daemonFrame = frame;
        onFrameSettled(entry.leafKey, frame);
      }
    }, FRAME_SETTLE_MS);
  };
  const handleClose = (event: Event) => {
    const entry = entryFor(event);
    if (entry?.leafKey) onCloseRequested(entry.leafKey);
  };
  window.addEventListener('attn:tile-window-frame', handleFrame);
  window.addEventListener('attn:tile-window-close', handleClose);
  return () => {
    window.removeEventListener('attn:tile-window-frame', handleFrame);
    window.removeEventListener('attn:tile-window-close', handleClose);
  };
}
