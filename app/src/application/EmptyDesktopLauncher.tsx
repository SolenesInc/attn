import { useCallback, useEffect, useRef, useState } from 'react';
import { LocationPicker } from '../components/LocationPicker';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import type { SessionAgent } from '../types/sessionAgent';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppShell,
  useChiefOfStaffContext,
  useSessionLaunchContext,
} from './AppContexts';
import './EmptyDesktopLauncher.css';

export function EmptyDesktopLauncher({ label, active }: { label: string; active: boolean }) {
  const { launchLocation, handleCreateWorktreeSession, registerInlineLauncher } = useSessionLaunchContext();
  const {
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    getRepoInfo,
    sendCreateWorktree,
    sendDeleteWorktree,
  } = useDaemonApi();
  const { agentAvailability } = useAppShell();
  const { showError } = useAppErrorsContext();
  const { settings, daemonEndpoints } = useAppInputs();
  const { hasChiefOfStaff } = useChiefOfStaffContext();
  const rootRef = useRef<HTMLDivElement>(null);
  const [launching, setLaunching] = useState<string | null>(null);
  // The dialog unmounts after a pick; the inline picker starts over instead.
  const [pickerGeneration, setPickerGeneration] = useState(0);

  useEffect(() => {
    if (!active || launching) return;
    const focusLauncher = () => {
      rootRef.current?.querySelector<HTMLElement>('[data-testid="location-picker-path-input"], [data-testid="repo-options"]')?.focus();
    };
    focusLauncher();
    registerInlineLauncher(focusLauncher);
    return () => registerInlineLauncher(null);
  }, [active, launching, registerInlineLauncher]);

  const handleSelect = useCallback(
    (path: string, agent: SessionAgent, endpointId?: string, yoloMode?: boolean, chiefOfStaff?: boolean, autoMode?: boolean) => {
      setLaunching(path.split('/').pop() || path);
      void launchLocation(path, agent, endpointId, yoloMode, chiefOfStaff, autoMode)
        .finally(() => setLaunching(null));
    },
    [launchLocation],
  );

  return (
    <div className="empty-desktop-launcher" data-testid="empty-desktop-launcher" ref={rootRef}>
      {launching ? (
        <div className="empty-desktop-launcher-starting" role="status">
          Starting {launching}…
        </div>
      ) : (
        <LocationPicker
          key={pickerGeneration}
          isOpen
          variant="inline"
          active={active}
          title={`New agent on ${label}`}
          onClose={() => setPickerGeneration((generation) => generation + 1)}
          onSelect={handleSelect}
          onGetRecentLocations={sendGetRecentLocations}
          onBrowseDirectory={sendBrowseDirectory}
          onInspectPath={sendInspectPath}
          onGetRepoInfo={getRepoInfo}
          onCreateWorktree={sendCreateWorktree}
          onCreateWorktreeSession={handleCreateWorktreeSession}
          onDeleteWorktree={sendDeleteWorktree}
          onError={showError}
          projectsDirectory={settings.projects_directory}
          agentAvailability={agentAvailability}
          endpoints={daemonEndpoints}
          chiefExists={hasChiefOfStaff}
        />
      )}
    </div>
  );
}
