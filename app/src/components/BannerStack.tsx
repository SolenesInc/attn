import { Fragment, useEffect, useRef, useState, type RefObject } from 'react';
import { renderWarningSegments } from '../utils/warningLinks';

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

// useBannerHeight tracks a banner's border-box height. Null while the banner
// is absent or unmeasurable; then the fixed CSS offsets apply as before.
function useBannerHeight(active: boolean): [RefObject<HTMLDivElement | null>, number | null] {
  const ref = useRef<HTMLDivElement | null>(null);
  const [height, setHeight] = useState<number | null>(null);
  useEffect(() => {
    setHeight(null);
    if (!active || !ref.current || typeof ResizeObserver === 'undefined') return;
    const el = ref.current;
    setHeight(el.offsetHeight);
    const observer = new ResizeObserver((entries) => {
      const target = entries[entries.length - 1]?.target as HTMLElement | undefined;
      setHeight(target?.offsetHeight ?? el.offsetHeight);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [active]);
  return [ref, height];
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
  const [connectionRef, connectionHeight] = useBannerHeight(connectionError != null);
  const [warningRef, warningHeight] = useBannerHeight(warnings.length > 0);
  // Inline offsets only once every banner above is measured; otherwise the
  // fixed with-connection-error/with-warning classes hold the first paint.
  const connH = connectionError ? connectionHeight : 0;
  const warnH = warnings.length > 0 ? warningHeight : 0;
  const warningTop = connH;
  const updateTop = connH != null && warnH != null ? connH + warnH : null;

  return (
    <>
      {connectionError && (
        <div ref={connectionRef} className="connection-error-banner">
          {connectionError}
        </div>
      )}
      {warnings.length > 0 && (
        <div
          ref={warningRef}
          className={`warning-banner ${connectionError ? 'with-connection-error' : ''}`}
          style={warningTop != null ? { top: warningTop } : undefined}
        >
          <span>
            {warnings.map((w, i) => (
              <Fragment key={`${w.code}-${i}`}>
                {renderWarningSegments(w.message, onOpenWarningUrl)}
                {i < warnings.length - 1 ? ' ' : null}
              </Fragment>
            ))}
          </span>
          <button className="warning-dismiss" onClick={onClearWarnings} title="Dismiss">
            ×
          </button>
        </div>
      )}
      {updateAvailableVersion && (
        <div
          className={`update-banner ${connectionError ? 'with-connection-error' : ''} ${warnings.length > 0 ? 'with-warning' : ''}`}
          style={updateTop != null ? { top: updateTop } : undefined}
        >
          <span>Version {updateAvailableVersion} is available on GitHub.</span>
          <button type="button" className="update-install" onClick={() => void onOpenLatestRelease()}>
            View Release
          </button>
          <button
            type="button"
            className="update-dismiss"
            onClick={onDismissLatestRelease}
            title="Dismiss"
            aria-label="Dismiss update banner"
          >
            ×
          </button>
        </div>
      )}
    </>
  );
}
