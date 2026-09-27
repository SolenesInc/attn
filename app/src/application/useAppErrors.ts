import { useCallback, useEffect } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { UI_DIAGNOSTICS_FILE_DISPLAY } from '../utils/uiDiagnosticsLog';
import { AppContentProps } from './appSupport';

interface Options {
  settingError: AppContentProps['settingError'];
  clearSettingError: AppContentProps['clearSettingError'];
}
export function useAppErrors({ settingError, clearSettingError }: Options) {
  const { disconnectExplanation, clearDisconnectExplanation, sendBootstrapEndpoint } =
    useDaemonApi();
  const { toast, showError, showNotice, clearToast } = useToast();
  const handleTerminalModelRecovered = useCallback(() => {
    showError(
      `Terminal issue recovered. We reloaded it for you. Diagnostics were saved to ${UI_DIAGNOSTICS_FILE_DISPLAY}; please send this file to Victor so he can troubleshoot it.`,
      { durationMs: 12_000 },
    );
  }, [showError]);

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

  useEffect(() => {
    if (!disconnectExplanation) {
      return;
    }
    showError(disconnectExplanation, { durationMs: 8000 });
    clearDisconnectExplanation();
  }, [clearDisconnectExplanation, disconnectExplanation, showError]);

  return {
    toast,
    showError,
    showNotice,
    clearToast,
    handleTerminalModelRecovered,
    handleRebootstrapEndpoint,
  };
}
