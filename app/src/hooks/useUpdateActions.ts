import { useMemo } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';

export function useUpdateActions() {
  const { sendStartUpdate, sendDeclineUpdate } = useDaemonApi();
  const { showError } = useToast();
  return useMemo(() => {
    const refused = (err: Error) => showError(err.message);
    return {
      startUpdate: (requestId?: string) => void sendStartUpdate(requestId).catch(refused),
      declineUpdate: (requestId: string) => void sendDeclineUpdate(requestId).catch(refused),
    };
  }, [sendStartUpdate, sendDeclineUpdate, showError]);
}
