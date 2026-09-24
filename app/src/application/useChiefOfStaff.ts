import { useCallback, useMemo, useState } from 'react';
import { useErrorToast } from '../components/ErrorToast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { AppContentProps } from './appSupport';
import { useAppSessions } from './useAppSessions';

interface Options {
  enrichedLocalSessions: ReturnType<typeof useAppSessions>['enrichedLocalSessions'];
  daemonSessions: AppContentProps['daemonSessions'];
  showError: ReturnType<typeof useErrorToast>['showError'];
}
export function useChiefOfStaff({ enrichedLocalSessions, daemonSessions, showError }: Options) {
  const { sendSetChiefOfStaff } = useDaemonApi();
  const [chiefTransferTarget, setChiefTransferTarget] = useState<{
    sessionId: string;
    targetLabel: string;
    currentLabel: string;
  } | null>(null);
  const [chiefTransferSaving, setChiefTransferSaving] = useState(false);
  const applyChiefOfStaffChange = useCallback(
    async (sessionId: string, enabled: boolean) => {
      try {
        await sendSetChiefOfStaff(sessionId, enabled);
      } catch (err) {
        showError(err instanceof Error ? err.message : 'Chief of staff update failed.');
        throw err;
      }
    },
    [sendSetChiefOfStaff, showError],
  );

  const handleChangeChiefOfStaff = useCallback(
    (sessionId: string, enabled: boolean) => {
      const target = enrichedLocalSessions.find((session) => session.id === sessionId);
      if (!target) {
        showError('Session not found.');
        return;
      }
      if (!enabled) {
        void applyChiefOfStaffChange(sessionId, false).catch(() => {});
        return;
      }
      const current = enrichedLocalSessions.find((session) => session.chiefOfStaff);
      if (current && current.id !== sessionId) {
        setChiefTransferTarget({
          sessionId,
          targetLabel: target.label,
          currentLabel: current.label,
        });
        return;
      }
      void applyChiefOfStaffChange(sessionId, true).catch(() => {});
    },
    [applyChiefOfStaffChange, enrichedLocalSessions, showError],
  );

  const handleConfirmChiefTransfer = useCallback(async () => {
    if (!chiefTransferTarget || chiefTransferSaving) return;
    setChiefTransferSaving(true);
    try {
      await applyChiefOfStaffChange(chiefTransferTarget.sessionId, true);
      setChiefTransferTarget(null);
    } catch {
    } finally {
      setChiefTransferSaving(false);
    }
  }, [applyChiefOfStaffChange, chiefTransferSaving, chiefTransferTarget]);

  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const hasChiefOfStaff = useMemo(
    () => daemonSessions.some((ds) => ds.chief_of_staff === true && ds.profile_id === selectedProfileId),
    [daemonSessions, selectedProfileId],
  );

  return {
    chiefTransferTarget,
    chiefTransferSaving,
    setChiefTransferTarget,
    handleChangeChiefOfStaff,
    handleConfirmChiefTransfer,
    hasChiefOfStaff,
  };
}
