import { useCallback, useEffect } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { AppContentProps } from './appSupport';

interface Options {
  settingError: AppContentProps['settingError'];
  clearSettingError: AppContentProps['clearSettingError'];
}
export function useAppErrors({ settingError, clearSettingError }: Options) {
  const { sendBootstrapEndpoint } = useDaemonApi();
  const { showError } = useToast();
  const handleRebootstrapEndpoint = useCallback(
    async (endpointId: string) => {
      try {
        await sendBootstrapEndpoint(endpointId);
      } catch (err) {
        showError(err instanceof Error ? err.message : 'Sync failed.');
      }
    },
    [sendBootstrapEndpoint, showError],
  );
  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

  return {
    showError,
    handleRebootstrapEndpoint,
  };
}
