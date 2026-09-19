import { createContext, useContext, useLayoutEffect, useRef, useState } from 'react';
import { DelegationChainController, type OpenChain } from './delegationChainController';

export const DelegationChainContext = createContext<{ controller: DelegationChainController; open: OpenChain | null } | null>(null);

export function useDelegationChainControl() {
  return useContext(DelegationChainContext)?.controller ?? null;
}

export function useDelegationChainTrigger(sessionId: string, kind: 'row' | 'badge', onOpen?: () => void) {
  const context = useContext(DelegationChainContext);
  const controller = context?.controller;
  const [element, setElement] = useState<HTMLElement | null>(null);
  const notifyOpen = useRef(onOpen);
  useLayoutEffect(() => { notifyOpen.current = onOpen; }, [onOpen]);
  useLayoutEffect(() => {
    if (!element || !controller) return;
    const anchor = kind === 'row' ? element.closest<HTMLElement>('.session-item') ?? element : element;
    const enter = (event: PointerEvent) => {
      if (event.pointerType === 'touch') return;
      notifyOpen.current?.();
      controller.show(sessionId, anchor, 'hover');
    };
    const leave = () => controller.leave(sessionId, anchor);
    anchor.addEventListener('pointerenter', enter);
    anchor.addEventListener('pointerleave', leave);
    if (kind === 'row') anchor.addEventListener('pointerdown', leave);
    return () => {
      anchor.removeEventListener('pointerenter', enter);
      anchor.removeEventListener('pointerleave', leave);
      if (kind === 'row') anchor.removeEventListener('pointerdown', leave);
      controller.detach(anchor);
    };
  }, [controller, sessionId, kind, element]);
  const pin = (anchor: HTMLElement) => {
    notifyOpen.current?.();
    controller?.show(sessionId, anchor, 'pinned');
  };
  return { ref: setElement, pin, expanded: context?.open?.sessionId === sessionId };
}
