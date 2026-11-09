import { Fragment } from 'react';
import { renderWarningSegments } from '../utils/warningLinks';
import { AvailableUpdateBanner } from './AvailableUpdateBanner';
import { OutpostUpdateBanner } from './OutpostUpdateBanner';
import { UpdateRunBanner } from './UpdateRunBanner';
import { SharedCodexBanner } from './SharedCodexBanner';

export interface BannerStackWarning {
  code: string;
  message: string;
}

interface BannerStackProps {
  connectionError: string | null;
  warnings: BannerStackWarning[];
  onOpenWarningUrl: (url: string) => void;
  onClearWarnings: () => void;
}

export function BannerStack({
  connectionError,
  warnings,
  onOpenWarningUrl,
  onClearWarnings,
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
      <AvailableUpdateBanner />
      <OutpostUpdateBanner />
      <UpdateRunBanner />
      <SharedCodexBanner />
    </div>
  );
}
