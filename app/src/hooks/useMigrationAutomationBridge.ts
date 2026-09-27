import { useCallback } from 'react';
import { useProfilesStore } from '../store/profiles';
import {
  APP_BUILD_IDENTITY,
  NOT_A_DOM_ACTION,
  runDomAutomationAction,
  useAutomationRequestListener,
  type AutomationRequest,
} from './uiAutomationDomActions';

export function useMigrationAutomationBridge() {
  const handleAutomationRequest = useCallback(async (request: AutomationRequest) => {
    const domResult = await runDomAutomationAction(request.action, request.payload || {});
    if (domResult !== NOT_A_DOM_ACTION) return domResult;
    const { migrationPhase, migration } = useProfilesStore.getState();
    switch (request.action) {
      case 'get_state':
        return { appBuild: APP_BUILD_IDENTITY, migrationPhase, sessions: [] };
      case 'migration_get_state':
        return { migrationPhase, migration };
      default:
        throw new Error(
          `${request.action} is unavailable while attn shows the workspace migration; only DOM actions, get_state and migration_get_state answer until the user continues past it`,
        );
    }
  }, []);
  useAutomationRequestListener(handleAutomationRequest);
}
