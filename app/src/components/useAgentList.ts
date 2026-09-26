import { useCallback, useRef, useState } from 'react';
import { flushSync } from 'react-dom';

const AGENT_FILTER_SELECTOR = '[data-testid="queue-agent-filter"]';
const AGENT_LIST_TOGGLE_SELECTOR = '[data-testid="queue-agents-toggle"]';

function focusedElement() {
  return document.activeElement instanceof HTMLElement && document.activeElement !== document.body
    ? document.activeElement
    : null;
}

export function useAgentList() {
  const [agentListOpen, setAgentListOpen] = useState(false);
  const focusBeforeOpen = useRef<HTMLElement | null>(null);

  const toggleAgentList = useCallback(() => {
    if (agentListOpen) {
      const returnTo = focusBeforeOpen.current?.isConnected
        ? focusBeforeOpen.current
        : document.querySelector<HTMLElement>(AGENT_LIST_TOGGLE_SELECTOR);
      focusBeforeOpen.current = null;
      returnTo?.focus();
      setAgentListOpen(false);
      return;
    }
    focusBeforeOpen.current = focusedElement();
    flushSync(() => setAgentListOpen(true));
    document.querySelector<HTMLElement>(AGENT_FILTER_SELECTOR)?.focus();
  }, [agentListOpen]);

  return { agentListOpen, toggleAgentList };
}
