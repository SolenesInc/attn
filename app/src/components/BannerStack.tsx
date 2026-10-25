import { Fragment } from 'react';
import { renderWarningSegments } from '../utils/warningLinks';
import { SharedCodexBanner } from './SharedCodexBanner';

export interface BannerStackWarning {
  code: string;
  message: string;
}

interface BannerStackProps {
  connectionError: string | null;
  warnings: BannerStackWarning[];
  updateAvailableVersion: string | null;
  onOpenWarningUrl: (url: string) => void;
  onClearWarnings: () => void;
  onOpenLatestRelease: () => Promise<void>;
  onDismissLatestRelease: () => void;
}

export function BannerStack({
  connectionError,
  warnings,
  updateAvailableVersion,
  onOpenWarningUrl,
  onClearWarnings,
  onOpenLatestRelease,
  onDismissLatestRelease,
}: BannerStackProps) {
  return (
    <div className="banner-stack">
      {connectionError && (
        <div className="connection-error-banner">
          {connectionError}
        </div>
      )}
      {warnings.length > 0 && (
        <div className="warning-banner">
          <span>
            {warnings.map((w, i) => (
              <Fragment key={`${w.code}-${i}`}>
                {renderWarningSegments(w.message, onOpenWarningUrl)}
                {i < warnings.length - 1 ? ' ' : null}
              </Fragment>
            ))}
          </span>
          <button className="banner-dismiss" onClick={onClearWarnings} title="Dismiss">
            ×
          </button>
        </div>
      )}
      {updateAvailableVersion && (
        <div className="update-banner">
          <span>Version {updateAvailableVersion} is available on GitHub.</span>
          <button type="button" className="banner-button" onClick={() => void onOpenLatestRelease()}>
            View Release
          </button>
          <button
            type="button"
            className="banner-dismiss"
            onClick={onDismissLatestRelease}
            title="Dismiss"
            aria-label="Dismiss update banner"
          >
            ×
          </button>
        </div>
      )}
      <SharedCodexBanner />
    </div>
  );
}
