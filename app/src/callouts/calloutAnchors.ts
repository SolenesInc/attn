import { CalloutAnchor, type CalloutStep } from '../types/generated';

export type FixedCalloutAnchor = Exclude<CalloutAnchor, CalloutAnchor.Agent | CalloutAnchor.Desktop>;
type AnchorFinder = () => HTMLElement[];

const elements = (selector: string) => Array.from(document.querySelectorAll<HTMLElement>(selector));
const named = (name: FixedCalloutAnchor): AnchorFinder => () => elements(`[data-callout-anchor="${name}"]`);

export const calloutAnchors: Record<FixedCalloutAnchor, AnchorFinder> = {
  sidebar: named(CalloutAnchor.Sidebar),
  home: named(CalloutAnchor.Home),
  commands: named(CalloutAnchor.Commands),
  garden: named(CalloutAnchor.Garden),
  desktops: named(CalloutAnchor.Desktops),
  queue: named(CalloutAnchor.Queue),
  crew: named(CalloutAnchor.Crew),
  'settings-button': named(CalloutAnchor.SettingsButton),
};

export function isFixedAnchor(anchor: CalloutAnchor): anchor is FixedCalloutAnchor {
  return anchor !== CalloutAnchor.Agent && anchor !== CalloutAnchor.Desktop;
}

function visibleRect(element: HTMLElement): DOMRect | null {
  if (element.closest('.view-container:not(.visible), [inert], [hidden]')) return null;
  const rect = element.getBoundingClientRect();
  let left = Math.max(0, rect.left);
  let top = Math.max(0, rect.top);
  let right = Math.min(window.innerWidth, rect.right);
  let bottom = Math.min(window.innerHeight, rect.bottom);
  for (let parent: HTMLElement | null = element; parent; parent = parent.parentElement) {
    const style = getComputedStyle(parent);
    if (style.display === 'none' || style.visibility === 'hidden') return null;
    if (parent === element) continue;
    const bounds = parent.getBoundingClientRect();
    if (/auto|scroll|hidden|clip/.test(style.overflowX)) {
      left = Math.max(left, bounds.left);
      right = Math.min(right, bounds.right);
    }
    if (/auto|scroll|hidden|clip/.test(style.overflowY)) {
      top = Math.max(top, bounds.top);
      bottom = Math.min(bottom, bounds.bottom);
    }
  }
  return right > left && bottom > top ? new DOMRect(left, top, right - left, bottom - top) : null;
}

export function calloutElementVisible(element: HTMLElement): boolean {
  return visibleRect(element) !== null;
}

export function calloutElements(step: CalloutStep): HTMLElement[] {
  if (isFixedAnchor(step.anchor)) return calloutAnchors[step.anchor]();
  const attribute = step.anchor === CalloutAnchor.Agent ? 'data-session-id' : 'data-desktop-id';
  const id = step.session_id ?? step.desktop_id;
  return elements(`[data-callout-anchor="sidebar"] [${attribute}]`)
    .filter((element) => element.getAttribute(attribute) === id);
}

export function calloutTarget(step: CalloutStep) {
  const targets = calloutElements(step).map((element) => ({ element, rect: visibleRect(element) }))
    .filter((target): target is { element: HTMLElement; rect: DOMRect } => target.rect !== null);
  if (!targets.length) return null;
  const rects = targets.map((target) => target.rect);
  const left = Math.max(0, Math.min(...rects.map((rect) => rect.left)));
  const top = Math.max(0, Math.min(...rects.map((rect) => rect.top)));
  const right = Math.min(window.innerWidth, Math.max(...rects.map((rect) => rect.right)));
  const bottom = Math.min(window.innerHeight, Math.max(...rects.map((rect) => rect.bottom)));
  return { elements: targets.map((target) => target.element), rect: new DOMRect(left, top, right - left, bottom - top) };
}

export function onScreenCalloutAnchors(): FixedCalloutAnchor[] {
  return (Object.keys(calloutAnchors) as FixedCalloutAnchor[])
    .filter((anchor) => calloutAnchors[anchor]().some(calloutElementVisible));
}
