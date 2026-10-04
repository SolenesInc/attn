import type { RefObject } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { invoke } from '@tauri-apps/api/core';
import { getCurrentWebviewWindow } from '@tauri-apps/api/webviewWindow';
import type { QuickCaptureClient, QuickCaptureItem } from './client';
import type { QuickCaptureAttachment, AttachmentMotion } from '../components/QuickCaptureAttachmentPreview';
type QuickCaptureExpectation = { frame?: boolean; visible?: boolean; settled?: boolean; imageCount?: number; attachmentCount?: number; view?: 'compose' | 'recent'; recentText?: string; selectedText?: string; composing?: boolean; mailbox?: string; fontScale?: number };

interface QuickCaptureAutomationOptions {
 editor: RefObject<HTMLTextAreaElement | null>;
 state: RefObject<{ text: string; mailbox: string; files: QuickCaptureAttachment[]; saved: QuickCaptureItem[]; binding: string; uncertain: boolean; restored: boolean }>;
 composing: RefObject<boolean>;
 motion: RefObject<(AttachmentMotion | { kind: 'drop'; phase: 'start' | 'end'; at: number })[]>;
 latency: RefObject<{ openedAt: number; focusedAt: number; nativeShowToFocusMs: number }[]>;
 client: RefObject<QuickCaptureClient>;
 stageDraft: () => Promise<void>;
}
export function installQuickCaptureAutomationBridge(options: QuickCaptureAutomationOptions): () => void {
    const inputTrace: object[] = [];
    const traceInput = (event: Event) => inputTrace.push({ at: Date.now(), type: event.type,
      key: event instanceof KeyboardEvent ? event.key : undefined,
      data: event instanceof InputEvent ? event.data : undefined, focused: document.activeElement === options.editor.current,
      readOnly: options.editor.current?.readOnly, value: options.editor.current?.value });
    ['keydown', 'beforeinput', 'input'].forEach(type => document.addEventListener(type, traceInput, true));
    const automation = listen<{ request_id: string; action: string; payload: { binding?: string; staged?: boolean } & QuickCaptureExpectation }>('attn://capture/automation', async ({ payload }) => {
      if (!payload.action.startsWith('capture_')) return;
      let result: unknown;
      let error: string | undefined;
      try {
        if (payload.action === 'capture_state') {
          if (payload.payload.frame) await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
          if (payload.payload.attachmentCount !== undefined || payload.payload.imageCount !== undefined || payload.payload.view || payload.payload.recentText || payload.payload.selectedText || payload.payload.visible !== undefined || payload.payload.composing !== undefined || payload.payload.mailbox !== undefined || payload.payload.fontScale !== undefined) await waitForCaptureView(payload.payload);
          if (payload.payload.staged) { await options.stageDraft(); }
          const bounds = (element: Element | null) => { const rect = element?.getBoundingClientRect(); return rect ? { x: (rect.x + rect.width / 2) / window.innerWidth, y: (rect.y + rect.height / 2) / window.innerHeight } : null; };
          const nativeDiagnostics = await invoke<{ diagnostics?: string[] }>('capture_status');
          result = { recentRows: [...document.querySelectorAll('.capture-history-item')].map(row => ({ text: row.textContent, selected: row.getAttribute('aria-current') === 'true', captureId: row.getAttribute('data-capture-id'), buttons: [...row.querySelectorAll('button')].map(button => button.textContent) })), inputTrace, nativeDiagnostics: nativeDiagnostics.diagnostics, visibility: document.visibilityState, nativeFocused: await getCurrentWebviewWindow().isFocused(), fontScale: Number(document.querySelector<HTMLElement>('.capture')?.dataset.fontScale), editorFontSize: options.editor.current ? getComputedStyle(options.editor.current).fontSize : null, view: document.querySelector('.capture-history') ? 'recent' : 'compose', flyingFiles: document.querySelectorAll('.capture-flight').length, controls: { recent: bounds(document.querySelector('[aria-label="Recent messages"]')), remove: bounds(document.querySelector('.capture-files button')), editor: bounds(options.editor.current), mailbox: bounds(document.querySelector('[aria-label="Send to"]')) }, ...options.state.current, composing: options.composing.current, files: options.state.current.files.map(({ name }) => ({ name })), motion: options.motion.current, reducedMotion: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false, activeAnimations: document.getAnimations().filter(animation => animation.playState === 'running').length, latencyMs: options.latency.current, focused: document.activeElement === options.editor.current, visible: await getCurrentWebviewWindow().isVisible() };

        } else if (payload.action === 'capture_binding') {
          await options.client.current.setBinding(payload.payload.binding || null);
          result = { binding: payload.payload.binding || '' };
        } else if (payload.action === 'capture_dismiss') { await invoke('capture_hide'); result = {}; }
        else throw new Error(`Unknown capture action ${payload.action}`);
      } catch (e) { error = String(e); }
      await emit('attn://ui-automation/response', { request_id: payload.request_id, ok: !error, result, error });
    });
 return () => {
  ['keydown', 'beforeinput', 'input'].forEach(type => document.removeEventListener(type, traceInput, true));
  void automation.then(unlisten => unlisten());
 };
}
function waitForCaptureView({ imageCount: count, attachmentCount, view, recentText, selectedText, settled, visible, composing, mailbox, fontScale }: QuickCaptureExpectation): Promise<void> {
  return new Promise((resolve, reject) => {
    const finish = (error?: string) => { observer.disconnect(); document.removeEventListener('load', check, true); document.removeEventListener('error', check, true); document.removeEventListener('focusin', check, true); if (error) reject(new Error(error)); else resolve(); };
    const check = () => {
      const images = [...document.querySelectorAll<HTMLImageElement>('.capture-files img')];
      const error = document.querySelector('[role="alert"]')?.textContent;
      if (count !== undefined && (error || images.length > count)) { finish(`Expected ${count} decoded image previews, observed ${images.length}. ${error || ''}`); return; }
      const files = [...document.querySelectorAll<HTMLElement>('.capture-files figure')];
      const filesReady = attachmentCount === undefined || (files.length === attachmentCount && files.every(file => file.dataset.fileReady === 'true'));
      const target = document.querySelector('.capture textarea');
      const recentView = document.querySelector('.capture-history');
      const viewReady = !view || (view === 'recent' ? recentView?.contains(document.activeElement) : target && document.activeElement === target);
      const recentReady = !recentText || Boolean(document.querySelector('.capture-history-list')?.textContent?.includes(recentText));
      const selectedReady = !selectedText || Boolean(document.querySelector('.capture-history-item[aria-current=true]')?.textContent?.includes(selectedText));
      const imagesReady = count === undefined || (images.length === count && images.every(image => image.complete && image.naturalWidth > 0));
      const motionReady = !settled || !document.querySelector('.capture-files .arriving');
      const visibleReady = visible === undefined || document.querySelector<HTMLElement>(".capture")!.dataset.captureVisible === String(visible);
      const mailboxReady = mailbox === undefined || document.querySelector<HTMLElement>('.capture')?.dataset.mailbox === mailbox;
      const fontReady = fontScale === undefined || Number(document.querySelector<HTMLElement>('.capture')?.dataset.fontScale) === fontScale;
      if (filesReady && recentReady && selectedReady && fontReady && viewReady && imagesReady && motionReady && visibleReady && mailboxReady && (composing === undefined || (document.querySelector<HTMLElement>(".capture textarea")?.dataset.composing === "true") === composing)) finish();
    };
    const observer = new MutationObserver(check);
    observer.observe(document.querySelector('.capture')!, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'data-capture-visible', 'data-composing', 'data-mailbox', 'data-font-scale', 'data-file-ready', 'aria-current'] });
    document.addEventListener('load', check, true); document.addEventListener('error', check, true); document.addEventListener('focusin', check, true);
    check();
  });
}
