import { useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { updateRunIsOpen } from './UpdateRunBanner';

const INSTALL_LABELS: Record<string, string> = {
  release_app: 'Release app',
  release_binary: 'Release binary',
  source: 'Built from source',
  named_instance: 'Named instance',
  outpost: 'Outpost',
  unknown: 'Unknown',
};

function availability(settings: Record<string, string>): string {
  if (settings.update_available_version) return settings.update_available_version;
  if (settings.update_check_error) return `Unknown. The last check failed: ${settings.update_check_error}`;
  if (settings.update_checked_at) return 'Up to date';
  return settings.update_install_kind === 'source' ? 'Source installs do not check for releases' : 'Not checked';
}

const UPDATABLE_KINDS = ['source', 'named_instance'];

function UpdateAction() {
  const api = useOptionalDaemonApi();
  const [error, setError] = useState('');
  if (!api || !UPDATABLE_KINDS.includes(api.settings.update_install_kind)) return null;
  const running = !!api.updateRun && updateRunIsOpen(api.updateRun);
  return (
    <div className="settings-update-action">
      <button
        type="button"
        className="banner-button"
        disabled={running}
        onClick={() => {
          setError('');
          api.sendStartUpdate().catch((err: Error) => setError(err.message));
        }}
      >
        {running ? 'Updating…' : 'Update attn'}
      </button>
      {error && <div className="settings-warning" role="alert">{error}</div>}
    </div>
  );
}

export function UpdateSettings({ settings }: { settings: Record<string, string> }) {
  return (
    <>
    <dl className="settings-update-facts" data-testid="settings-updates">
      <dt>Installed as</dt>
      <dd>{INSTALL_LABELS[settings.update_install_kind] ?? settings.update_install_kind}</dd>
      <dt>Build</dt>
      <dd>{settings.update_current_version} ({settings.update_current_commit?.slice(0, 9)})</dd>
      <dt>Available</dt>
      <dd>{availability(settings)}</dd>
    </dl>
    <UpdateAction />
    </>
  );
}
