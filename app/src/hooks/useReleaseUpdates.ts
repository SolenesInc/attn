import { getVersion } from '@tauri-apps/api/app';
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  getDismissedUpdateVersion,
  GitHubReleaseResponse,
  isNewerVersion,
  persistDismissedUpdateVersion,
  RELEASE_CHECK_INTERVAL_MS,
  RELEASES_LATEST_API,
  RELEASES_LATEST_WEB,
} from '../application/appSupport';
import { normalizeInstallChannel, shouldCheckForReleaseUpdates } from '../utils/installChannel';

export function useReleaseUpdates() {
  const [updateAvailableVersion, setUpdateAvailableVersion] = useState<string | null>(null);
  const updateReleaseUrl = useRef(RELEASES_LATEST_WEB);
  const [dismissedUpdateVersion, setDismissedUpdateVersion] = useState<string | null>(() =>
    getDismissedUpdateVersion(),
  );
  const installChannel = normalizeInstallChannel(import.meta.env.VITE_INSTALL_CHANNEL);

  useEffect(() => {
    if (!isTauri()) return;
    if (!shouldCheckForReleaseUpdates(installChannel)) {
      setUpdateAvailableVersion(null);
      return;
    }

    let cancelled = false;
    let intervalId: ReturnType<typeof setInterval> | undefined;

    const checkLatestRelease = async () => {
      try {
        const currentVersion = await getVersion();
        const response = await fetch(RELEASES_LATEST_API, {
          headers: {
            Accept: 'application/vnd.github+json',
          },
        });

        if (!response.ok) {
          throw new Error(`GitHub API returned ${response.status}`);
        }

        const latest = (await response.json()) as GitHubReleaseResponse;
        if (cancelled) return;

        if (latest.draft || latest.prerelease || !latest.tag_name) {
          setUpdateAvailableVersion(null);
          updateReleaseUrl.current = RELEASES_LATEST_WEB;
          return;
        }

        const releaseUrl = latest.html_url || RELEASES_LATEST_WEB;
        updateReleaseUrl.current = releaseUrl;

        const latestVersion = latest.tag_name.replace(/^v/, '');
        if (isNewerVersion(currentVersion, latest.tag_name)) {
          if (dismissedUpdateVersion === latestVersion) {
            setUpdateAvailableVersion(null);
            return;
          }

          setUpdateAvailableVersion(latestVersion);
          return;
        }

        setUpdateAvailableVersion(null);
      } catch (err) {
        if (cancelled) return;
        console.warn('[App] Failed to check latest release:', err);
      }
    };

    void checkLatestRelease();
    intervalId = setInterval(() => {
      void checkLatestRelease();
    }, RELEASE_CHECK_INTERVAL_MS);

    return () => {
      cancelled = true;
      if (intervalId) clearInterval(intervalId);
    };
  }, [dismissedUpdateVersion, installChannel]);

  const handleOpenLatestRelease = useCallback(async () => {
    try {
      await openUrl(updateReleaseUrl.current);
    } catch (err) {
      console.error('[App] Failed to open release URL:', err);
    }
  }, []);

  const handleDismissLatestRelease = useCallback(() => {
    if (!updateAvailableVersion) return;

    persistDismissedUpdateVersion(updateAvailableVersion);
    setDismissedUpdateVersion(updateAvailableVersion);
    setUpdateAvailableVersion(null);
  }, [updateAvailableVersion]);

  return { updateAvailableVersion, handleOpenLatestRelease, handleDismissLatestRelease };
}
