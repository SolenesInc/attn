import { useCallback, useRef, useState } from 'react';
import { flushSync } from 'react-dom';

const AGENT_FILTER_SELECTOR = '[data-testid="queue-agent-filter"]';
const AGENT_LIST_TOGGLE_SELECTOR = '[data-testid="queue-agents-toggle"]';
const AGENT_LIST_SELECTOR = '[data-testid="queue-agent-list"]';

function focusIsOnAgentList() {
  return document.activeElement?.closest(`${AGENT_LIST_SELECTOR}, ${AGENT_LIST_TOGGLE_SELECTOR}`) != null;
}

function focusedElement() {
  return document.activeElement instanceof HTMLElement && document.activeElement !== document.body
    ? document.activeElement
    : null;
}

export function useAgentList() {
  const [agentListOpen, setAgentListOpen] = useState(false);
  const focusBeforeOpen = useRef<HTMLElement | null>(null);

  const closeAgentList = useCallback(() => {
    const opener = focusBeforeOpen.current;
    focusBeforeOpen.current = null;
    if (focusIsOnAgentList()) {
      (opener?.isConnected ? opener : document.querySelector<HTMLElement>(AGENT_LIST_TOGGLE_SELECTOR))?.focus();
    }
    setAgentListOpen(false);
  }, []);

  const toggleAgentList = useCallback(() => {
    if (agentListOpen) {
      closeAgentList();
      return;
    }
    focusBeforeOpen.current = focusedElement();
    flushSync(() => setAgentListOpen(true));
    document.querySelector<HTMLElement>(AGENT_FILTER_SELECTOR)?.focus();
  }, [agentListOpen, closeAgentList]);

  return { agentListOpen, toggleAgentList, closeAgentList };
}
