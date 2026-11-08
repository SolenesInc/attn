import { openUrl } from '@tauri-apps/plugin-opener';
import { useState } from 'react';
import { getDismissedUpdateVersion, persistDismissedUpdateVersion } from '../application/appSupport';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';

export function AvailableUpdateBanner() {
  const settings = useOptionalDaemonApi()?.settings;
  const version = settings?.update_available_version;
  const url = settings?.update_available_url;
  const [dismissedVersion, setDismissedVersion] = useState(getDismissedUpdateVersion);
  if (!version || !url || version === dismissedVersion) return null;
  return (
    <div className="update-banner">
      <span>Version {version} is available on GitHub.</span>
      <button
        type="button"
        className="banner-button"
        onClick={() => void openUrl(url).catch(err => console.error('[App] Failed to open release URL:', err))}
      >
        View Release
      </button>
      <button
        type="button"
        className="banner-dismiss"
        onClick={() => {
          persistDismissedUpdateVersion(version);
          setDismissedVersion(version);
        }}
        title="Dismiss"
        aria-label="Dismiss update banner"
      >
        ×
      </button>
    </div>
  );
}
