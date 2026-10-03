import { useCallback, useLayoutEffect, useRef, type Ref, type RefCallback } from 'react';

const hosts: { token: symbol; element: HTMLElement }[] = [];
const listeners = new Set<() => void>();
const notify = () => listeners.forEach((listener) => listener());

export function toastHost(): HTMLElement {
  return hosts[hosts.length - 1]?.element ?? document.body;
}

export function subscribeToastHost(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

function registerHost(element: HTMLElement): () => void {
  const token = Symbol();
  const child = hosts.findIndex((host) => element.contains(host.element));
  hosts.splice(child === -1 ? hosts.length : child, 0, { token, element });
  notify();
  return () => {
    const index = hosts.findIndex((host) => host.token === token);
    if (index !== -1) hosts.splice(index, 1);
    notify();
  };
}

export function useToastHost<T extends HTMLElement>(active = true, forwardedRef?: Ref<T>): RefCallback<T> {
  const host = useRef<T | null>(null);
  useLayoutEffect(() => {
    if (active && host.current) return registerHost(host.current);
  }, [active]);
  return useCallback(
    (element: T | null) => {
      host.current = element;
      if (typeof forwardedRef === 'function') forwardedRef(element);
      else if (forwardedRef) forwardedRef.current = element;
    },
    [forwardedRef],
  );
}
