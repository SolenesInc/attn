import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';

export function useConversationChanges() {
  const api = useOptionalDaemonApi();
  return {
    conversationChangeSignal: api?.keptConversationsChangeSignal ?? 0,
    connectionGeneration: api?.connectionGeneration ?? 0,
  };
}
