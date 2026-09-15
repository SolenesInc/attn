import { useEffect, useMemo } from 'react';
import { listenPtyEvents, type PtyEventPayload } from '../../pty/bridge';

export interface PaneRuntimeEventBinding {
  sessionId?: string;
  paneId: string;
  runtimeId: string;
  onEvent: (event: PtyEventPayload) => void;
  isLive?: () => boolean;
}

export interface PaneRuntimeEventRouter {
  registerBinding: (binding: PaneRuntimeEventBinding) => () => void;
}

export interface PaneRuntimeEventRouterController extends PaneRuntimeEventRouter {
  dispose: () => void;
  handleEvent: (event: PtyEventPayload) => void;
}

export function createPaneRuntimeEventRouterController(): PaneRuntimeEventRouterController {
  const bindingsByRuntime = new Map<string, PaneRuntimeEventBinding[]>();

  const registerBinding = (binding: PaneRuntimeEventBinding) => {
    const bindings = bindingsByRuntime.get(binding.runtimeId) ?? [];
    bindingsByRuntime.set(binding.runtimeId, [...bindings, binding]);

    return () => {
      const remaining = bindingsByRuntime.get(binding.runtimeId)?.filter((entry) => entry !== binding);
      if (!remaining || remaining.length === 0) {
        bindingsByRuntime.delete(binding.runtimeId);
        return;
      }
      bindingsByRuntime.set(binding.runtimeId, remaining);
    };
  };

  const handleEvent = (event: PtyEventPayload) => {
    const bindings = bindingsByRuntime.get(event.id);
    if (!bindings) {
      return;
    }
    const authority = bindings.find((binding) => binding.isLive?.() ?? true) ?? bindings[0];
    for (const binding of bindings) {
      binding.onEvent(binding === authority || event.event !== 'data' ? event : { ...event, suppressResponses: true });
    }
  };

  const dispose = () => {
    bindingsByRuntime.clear();
  };

  return {
    registerBinding,
    handleEvent,
    dispose,
  };
}

export function usePaneRuntimeEventRouter(): PaneRuntimeEventRouter {
  const controller = useMemo(() => createPaneRuntimeEventRouterController(), []);

  useEffect(() => {
    let active = true;
    let disposeListener: (() => void) | null = null;

    void listenPtyEvents((event) => {
      controller.handleEvent(event.payload);
    }).then((dispose) => {
      if (!active) {
        dispose();
        return;
      }
      disposeListener = dispose;
    });

    return () => {
      active = false;
      disposeListener?.();
      controller.dispose();
    };
  }, [controller]);

  return controller;
}
