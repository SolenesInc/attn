import { useEffect, useLayoutEffect, useRef } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { isPresentWindowAction } from './usePresentAutomationBridge';
import { waitForAutomationDom } from './uiAutomationDom';
import {
  armNativePointerWitness,
  disarmNativePointerWitness,
  waitForNativePointerWitness,
} from './nativePointerWitness';
import { settleBeforeBridgeRequest, settleUi } from './uiAutomationSettle';

const UI_AUTOMATION_REQUEST_EVENT = 'attn://ui-automation/request';
const UI_AUTOMATION_RESPONSE_EVENT = 'attn://ui-automation/response';
const UI_AUTOMATION_READY_EVENT = 'attn://ui-automation/ready';

export interface AutomationRequest {
  request_id: string;
  action: string;
  payload?: Record<string, unknown> | null;
}

interface AutomationResponse {
  request_id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

function readBuildEnv(value: string | undefined): string | null {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

export const APP_BUILD_IDENTITY = {
  version: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_VERSION),
  sourceFingerprint: readBuildEnv(import.meta.env.VITE_ATTN_SOURCE_FINGERPRINT),
  gitCommit: readBuildEnv(import.meta.env.VITE_ATTN_GIT_COMMIT),
  buildTime: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_TIME),
};

export const NOT_A_DOM_ACTION = Symbol('not a DOM automation action');

export function rectSnapshot(element: Element | null) {
  if (!(element instanceof HTMLElement)) {
    return null;
  }
  const rect = element.getBoundingClientRect();
  return {
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}


export async function captureDomScreenshotData(selector?: string) {
  // An unresolved selector must NOT fall back to #root: that re-introduces the WebGL-canvas
  // serialization hang while hiding the real cause (the element asked for is not mounted).
  let target: HTMLElement;
  if (selector) {
    const selected = document.querySelector(selector);
    if (!(selected instanceof HTMLElement)) {
      throw new Error(`Screenshot selector not found in DOM: ${selector}`);
    }
    target = selected;
  } else {
    const root = document.getElementById('root') || document.body;
    if (!(root instanceof HTMLElement)) {
      throw new Error('Screenshot target not found');
    }
    target = root;
  }

  const { toPng } = await import('html-to-image');
  const backgroundColor = getComputedStyle(document.body).backgroundColor || '#111111';

  // A running CSS animation in the cloned subtree can leave html-to-image's serialized SVG
  // <image> in a never-settled load state in WebKit, so toPng hangs until the caller times out.
  const freeze = document.createElement('style');
  freeze.textContent =
    '*,*::before,*::after{animation:none!important;transition:none!important;}';
  document.head.appendChild(freeze);
  void document.body.offsetHeight;

  const options = {
    cacheBust: true,
    pixelRatio: 1,
    backgroundColor,
    // Embedding @font-face resources fetches each font and can hang indefinitely.
    skipFonts: true,
    filter: isScreenshotNode,
  };
  let dataUrl: string;
  try {
    dataUrl = await toPng(target, options);
  } catch (error) {
    throw new Error(await describeScreenshotFailure(target, selector ?? '#root', options, error));
  } finally {
    freeze.remove();
  }
  return {
    source: 'web',
    bounds: rectSnapshot(target),
    pngBase64: dataUrl.replace(/^data:image\/png;base64,/, ''),
  };
}

export function isScreenshotNode(node: HTMLElement): boolean {
  return !(node instanceof HTMLImageElement && !node.getAttribute('src'));
}

export async function describeScreenshotFailure(
  target: HTMLElement,
  label: string,
  options: Parameters<typeof import('html-to-image').toSvg>[1],
  error: unknown,
): Promise<string> {
  const bounds = target.getBoundingClientRect();
  const canvases = Array.from(target.querySelectorAll('canvas'));
  const where = `Screenshot of ${label} (${Math.round(bounds.width)}x${Math.round(bounds.height)}, ${canvases.length} canvases, visibility ${document.visibilityState})`;
  for (const canvas of canvases) {
    const canvasDataUrl = canvas.toDataURL();
    if (canvasDataUrl !== 'data:,' && !(await imageLoads(canvasDataUrl))) {
      return `${where}: the ${canvas.width}x${canvas.height} canvas image (${canvasDataUrl.length} chars) does not load`;
    }
  }
  const { toSvg } = await import('html-to-image');
  let svgDataUrl: string;
  try {
    svgDataUrl = await toSvg(target, options);
  } catch (svgError) {
    return `${where}: serializing the subtree failed: ${failureText(svgError)}; embedded images: ${describeEmbeddedImages(target)}`;
  }
  const xml = decodeURIComponent(svgDataUrl.slice(svgDataUrl.indexOf(',') + 1));
  const parserError = new DOMParser()
    .parseFromString(xml, 'image/svg+xml')
    .querySelector('parsererror')
    ?.textContent?.trim();
  if (parserError) {
    return `${where}: the serialized SVG (${xml.length} chars) does not parse: ${parserError}`;
  }
  return `${where}: the serialized SVG (${xml.length} chars) parses but loading it as an image failed: ${failureText(error)}`;
}

function describeEmbeddedImages(target: HTMLElement): string {
  const images = Array.from(target.querySelectorAll('img, image')).map((element) => {
    if (element instanceof HTMLImageElement) {
      const state = element.complete ? `${element.naturalWidth}x${element.naturalHeight}` : 'loading';
      const source = element.currentSrc || element.src || `(no src) ${element.outerHTML.slice(0, 160)}`;
      return `img ${source} ${state} in ${ancestorPath(element)}`;
    }
    return `image ${(element as SVGImageElement).href?.baseVal || '(no href)'} in ${ancestorPath(element)}`;
  });
  return images.length === 0 ? 'none' : images.join(', ');
}

function ancestorPath(element: Element): string {
  const names: string[] = [];
  for (let node = element.parentElement; node && names.length < 5; node = node.parentElement) {
    const className = typeof node.className === 'string' ? node.className.trim().split(/\s+/)[0] : '';
    names.push(className ? `${node.tagName.toLowerCase()}.${className}` : node.tagName.toLowerCase());
  }
  return names.join(' < ');
}

function imageLoads(src: string): Promise<boolean> {
  return new Promise((resolve) => {
    const image = new Image();
    image.onload = () => resolve(true);
    image.onerror = () => resolve(false);
    image.src = src;
  });
}

function failureText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (error instanceof Event) return `${error.type} event`;
  return String(error);
}


export interface ClickModifiers {
  meta?: boolean;
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
}

export function clickElementWithModifiers(element: HTMLElement, modifiers?: ClickModifiers) {
  const rect = element.getBoundingClientRect();
  const clientX = rect.x + rect.width / 2;
  const clientY = rect.y + rect.height / 2;
  const init: MouseEventInit = {
    bubbles: true,
    cancelable: true,
    view: window,
    clientX,
    clientY,
    metaKey: modifiers?.meta ?? false,
    ctrlKey: modifiers?.ctrl ?? false,
    shiftKey: modifiers?.shift ?? false,
    altKey: modifiers?.alt ?? false,
  };
  // Pointer events first, in the order a real browser fires them: anything listening for
  // pointerdown is otherwise invisible and the scenario reads as passing.
  const pointerInit: PointerEventInit = { ...init, pointerId: 1, pointerType: 'mouse', isPrimary: true };
  element.dispatchEvent(new PointerEvent('pointerdown', pointerInit));
  element.dispatchEvent(new MouseEvent('mousedown', init));
  element.dispatchEvent(new PointerEvent('pointerup', pointerInit));
  element.dispatchEvent(new MouseEvent('mouseup', init));
  element.dispatchEvent(new MouseEvent('click', init));
}

export function setInputValue(element: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value',
  )?.set;
  if (!setter) {
    throw new Error('Unable to resolve input value setter');
  }
  setter.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
}

// Bypass React's value-tracker via the native prototype setter, then fire both `input`
// and `change` so the component's onChange runs exactly as a user edit would.
export function setControlValue(
  element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string,
) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value')?.set;
  if (!setter) {
    throw new Error('Unable to resolve control value setter');
  }
  setter.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
  element.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
}



export async function runDomAutomationAction(
  action: string,
  payload: Record<string, unknown>,
): Promise<unknown> {
  switch (action) {
    case 'ping':
      return { pong: true };
    case 'capture_screenshot_data':
      return captureDomScreenshotData(
        typeof payload.selector === 'string' ? payload.selector : undefined,
      );
    case 'dom_click': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_click requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_click selector not found in DOM: ${selector}`);
      }
      const modifiers = (payload.modifiers ?? {}) as ClickModifiers;
      clickElementWithModifiers(element, modifiers);
      await settleUi(2);
      return { clicked: true, bounds: rectSnapshot(element) };
    }
    case 'dom_focus': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_focus requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_focus selector not found in DOM: ${selector}`);
      }
      element.focus();
      await settleUi(2);
      if (document.activeElement !== element) {
        throw new Error(`dom_focus target did not take focus: ${selector}`);
      }
      return { focused: true, tag: element.tagName };
    }
    case 'dom_active_element': {
      const active = document.activeElement;
      if (!(active instanceof HTMLElement)) return { tag: null };
      const field = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement ? active : null;
      return {
        tag: active.tagName,
        ...(typeof payload.selector === 'string' ? { matches: active.matches(payload.selector) } : {}),
        className: active.className,
        testId: active.getAttribute('data-testid'),
        selectionStart: field?.selectionStart ?? null,
        valueLength: field ? field.value.length : null,
      };
    }
    case 'dom_wait':
      return waitForAutomationDom({
        selector: typeof payload.selector === 'string' ? payload.selector : '',
        absent: payload.absent === true,
        textIncludes: typeof payload.textIncludes === 'string' ? payload.textIncludes : undefined,
        focused: payload.focused === true,
        timeoutMs: typeof payload.timeoutMs === 'number' ? payload.timeoutMs : NaN,
      });
    case 'dom_bounds': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_bounds requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_bounds selector not found in DOM: ${selector}`);
      }
      return { bounds: rectSnapshot(element) };
    }
    case 'dom_text': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_text requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_text selector not found in DOM: ${selector}`);
      }
      return { text: (element.textContent ?? '').replace(/\s+/g, ' ').trim() };
    }
    case 'dom_value': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_value requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement)) {
        throw new Error(`dom_value target is not a form control: ${selector}`);
      }
      return { value: element.value };
    }
    case 'dom_scroll_into_view': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_scroll_into_view requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_scroll_into_view selector not found in DOM: ${selector}`);
      }
      element.scrollIntoView({ block: 'center', behavior: 'auto' });
      await settleUi(2);
      return { scrolled: true, bounds: rectSnapshot(element) };
    }
    case 'dom_key': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      const key = typeof payload.key === 'string' ? payload.key : null;
      if (!selector) throw new Error('dom_key requires selector');
      if (!key) throw new Error('dom_key requires key');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_key selector not found in DOM: ${selector}`);
      }
      const modifiers = (payload.modifiers ?? {}) as ClickModifiers;
      element.focus();
      const init: KeyboardEventInit = {
        key,
        bubbles: true,
        cancelable: true,
        metaKey: modifiers.meta ?? false,
        ctrlKey: modifiers.ctrl ?? false,
        shiftKey: modifiers.shift ?? false,
        altKey: modifiers.alt ?? false,
      };
      const delivered = element.dispatchEvent(new KeyboardEvent('keydown', init));
      element.dispatchEvent(new KeyboardEvent('keyup', init));
      await settleUi(3);
      return { key, handled: !delivered };
    }
    case 'dom_hover': {
      // Both the pointer and mouse families are dispatched (handlers here come from either), and
      // enter/leave do not bubble, so the selector must name the element that actually listens.
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      if (!selector) throw new Error('dom_hover requires selector');
      const leave = payload.leave === true;
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`dom_hover selector not found in DOM: ${selector}`);
      }
      const rect = element.getBoundingClientRect();
      const init: PointerEventInit = {
        bubbles: false,
        cancelable: true,
        composed: true,
        pointerId: 1,
        pointerType: 'mouse',
        clientX: rect.left + rect.width / 2,
        clientY: rect.top + rect.height / 2,
      };
      if (leave) {
        element.dispatchEvent(new PointerEvent('pointerleave', init));
        element.dispatchEvent(new MouseEvent('mouseleave', init));
        element.dispatchEvent(new PointerEvent('pointerout', { ...init, bubbles: true }));
      } else {
        element.dispatchEvent(new PointerEvent('pointerover', { ...init, bubbles: true }));
        element.dispatchEvent(new PointerEvent('pointerenter', init));
        element.dispatchEvent(new MouseEvent('mouseenter', init));
      }
      await settleUi(2);
      return { hovered: !leave, bounds: rectSnapshot(element) };
    }
    case 'drag_dom': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      const dx = typeof payload.dx === 'number' ? payload.dx : 0;
      const dy = typeof payload.dy === 'number' ? payload.dy : 0;
      if (!selector) throw new Error('drag_dom requires selector');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLElement)) {
        throw new Error(`drag_dom selector not found in DOM: ${selector}`);
      }
      const rect = element.getBoundingClientRect();
      const from = { clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2 };
      element.dispatchEvent(new MouseEvent('mousedown', {
        bubbles: true, cancelable: true, view: window, button: 0, buttons: 1, ...from,
      }));
      // Two moves: a drag that arms on the first and applies on later ones would otherwise look like it worked.
      for (const step of [0.5, 1]) {
        window.dispatchEvent(new MouseEvent('mousemove', {
          bubbles: true, cancelable: true, view: window, buttons: 1,
          clientX: from.clientX + dx * step,
          clientY: from.clientY + dy * step,
        }));
        await settleUi(1);
      }
      window.dispatchEvent(new MouseEvent('mouseup', {
        bubbles: true, cancelable: true, view: window, button: 0,
        clientX: from.clientX + dx,
        clientY: from.clientY + dy,
      }));
      await settleUi(2);
      return { dragged: selector, from, dx, dy, bounds: rectSnapshot(element) };
    }
    case 'dom_type': {
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      const text = typeof payload.text === 'string' ? payload.text : null;
      if (!selector) throw new Error('dom_type requires selector');
      if (text === null) throw new Error('dom_type requires text');
      const element = document.querySelector(selector);
      if (!element) {
        throw new Error(`dom_type selector not found in DOM: ${selector}`);
      }
      if (element instanceof HTMLInputElement) {
        setInputValue(element, text);
      } else if (element instanceof HTMLTextAreaElement) {
        setControlValue(element, text);
      } else {
        throw new Error(`dom_type target is not an input or textarea: ${selector}`);
      }
      await settleUi(2);
      return { typed: text };
    }
    case 'dom_select': {
      // Selects need their own verb: dom_type goes through the input value setter, which a <select> ignores.
      const selector = typeof payload.selector === 'string' ? payload.selector : null;
      const value = typeof payload.value === 'string' ? payload.value : null;
      if (!selector) throw new Error('dom_select requires selector');
      if (value === null) throw new Error('dom_select requires value');
      const element = document.querySelector(selector);
      if (!(element instanceof HTMLSelectElement)) {
        throw new Error(`dom_select target is not a select: ${selector}`);
      }
      const offered = Array.from(element.options).map((option) => option.value);
      if (!offered.includes(value)) {
        throw new Error(`dom_select value ${value} is not offered by ${selector}; options: ${offered.join(', ')}`);
      }
      setControlValue(element, value);
      await settleUi(2);
      return { selected: value };
    }
    case 'get_window_bounds': {
      if (!isTauri()) {
        return null;
      }
      const appWindow = getCurrentWindow();
      const [scaleFactor, outerPosition, outerSize, minimized] = await Promise.all([
        appWindow.scaleFactor(),
        appWindow.outerPosition(),
        appWindow.outerSize(),
        appWindow.isMinimized(),
      ]);
      const logicalPosition = outerPosition.toLogical(scaleFactor);
      const logicalSize = outerSize.toLogical(scaleFactor);
      return {
        scaleFactor,
        minimized,
        logicalBounds: {
          x: logicalPosition.x,
          y: logicalPosition.y,
          width: logicalSize.width,
          height: logicalSize.height,
        },
      };
    }
    case 'arm_native_pointer_witness': {
      const selector = typeof payload.selector === 'string' ? payload.selector : '';
      if (!selector) throw new Error('arm_native_pointer_witness requires selector');
      armNativePointerWitness(selector);
      return { armed: true };
    }
    case 'wait_native_pointer_witness': {
      const receipt = await waitForNativePointerWitness();
      await settleUi();
      return receipt;
    }
    default:
      return NOT_A_DOM_ACTION;
  }
}

export function useAutomationRequestListener(handleAutomationRequest: (request: AutomationRequest) => Promise<unknown>) {
  const handleAutomationRequestRef = useRef(handleAutomationRequest);
  // The dispatcher dereferences this two frames after the request arrives, so only a committed
  // render may publish here: a concurrent render React discards must not become the handler.
  useLayoutEffect(() => {
    handleAutomationRequestRef.current = handleAutomationRequest;
  }, [handleAutomationRequest]);

  useEffect(() => {
    // Runtime gate injected by the Rust shell; the rule lives in
    // app/src-tauri/src/instance.rs::automation_enabled.
    const automationEnabled =
      typeof window !== 'undefined' && (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
    if (!isTauri() || !automationEnabled) {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      // The automation server broadcasts to ALL webview windows and resolves on the first response,
      // so exactly one listener answers; present_window_* belongs to usePresentAutomationBridge.
      if (isPresentWindowAction(request.action)) {
        return;
      }
      let response: AutomationResponse;
      try {
        // Settle before reading the ref: the wait spans renders, so a handler picked
        // beforehand would answer with a settled DOM and the props of an older render.
        await settleBeforeBridgeRequest(request.action);
        const result = await handleAutomationRequestRef.current(request);
        response = {
          request_id: request.request_id,
          ok: true,
          result,
        };
      } catch (error) {
        response = {
          request_id: request.request_id,
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
      await emit(UI_AUTOMATION_RESPONSE_EVENT, response);
    });

    return () => {
      disarmNativePointerWitness();
      void unlistenPromise.then((unlisten) => unlisten());
    };
  }, []);
}
