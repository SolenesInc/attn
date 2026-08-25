import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FocusEvent as ReactFocusEvent } from 'react';
import { AppViewRuntimeProvider, type AppViewRuntime } from '@victorarias/attn-app';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useDaemonStore } from '../../store/daemonSessions';
import { appBundleURL } from '../../utils/appBundle';
import { AppViewBoundary } from './AppViewBoundary';
import { AppViewLoadError, errorText, loadAppView, type AppViewComponent } from './loadAppView';
import './AppTileHost.css';
// SDK components are styled from attn's build, not the SDK's own chunk.
import './appSdkComponents.css';

// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md.

interface AppTileHostProps {
  app: string;
  view: string;
  workspaceId: string;
  sessionId: string | null;
  tileId: string;
  params: string;
}

interface Mounted {
  component: AppViewComponent;
  contentHash: string;
  versionId: number;
}

export function AppTileHost({ app, view, workspaceId, sessionId, tileId, params }: AppTileHostProps) {
  const { sendAppCommand, sendAppViewCrash, subscribeDocuments } = useDaemonApi();
  const entry = useDaemonStore((state) => state.apps.find((a) => a.name === app));

  const declared = entry?.views?.some((v) => v.name === view) ?? false;
  const servingHash = entry?.content_hash ?? '';
  const servingVersion = entry?.version_id ?? 0;
  const mountable = !!entry && entry.enabled && declared && servingHash !== '' && servingVersion !== 0;

  const [mounted, setMounted] = useState<Mounted | null>(null);
  const [loadError, setLoadError] = useState<AppViewLoadError | null>(null);
  // Part of the boundary's reset key, so a retry gets a fresh boundary rather
  // than the one still holding the last error.
  const [attempt, setAttempt] = useState(0);
  const [holdsFocus, setHoldsFocus] = useState(false);

  const stale = !!mounted && mountable && mounted.contentHash !== servingHash;
  const deferred = stale && holdsFocus;

  const wantedHash = deferred ? mounted.contentHash : servingHash;
  const wantedVersion = deferred ? mounted.versionId : servingVersion;

  useEffect(() => {
    if (!mountable || wantedHash === '') {
      setMounted(null);
      setLoadError(null);
      return;
    }
    let cancelled = false;
    setLoadError(null);
    void loadAppView(appBundleURL(app, wantedHash, view, attempt))
      .then((component) => {
        if (cancelled) return;
        setMounted({ component, contentHash: wantedHash, versionId: wantedVersion });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setMounted(null);
        setLoadError(error instanceof AppViewLoadError
          ? error
          : new AppViewLoadError('This view could not be loaded.', errorText(error)));
      });
    return () => {
      cancelled = true;
    };
  }, [app, view, wantedHash, wantedVersion, mountable, attempt]);

  const reportCrash = useCallback((error: Error, componentStack: string) => {
    if (!mounted) return;
    sendAppViewCrash({
      app,
      view,
      versionId: mounted.versionId,
      tileId,
      error: `${errorText(error)}\n\nComponent stack:${componentStack}`,
    });
  }, [app, view, tileId, mounted, sendAppViewCrash]);

  // Dropping `mounted` is load-bearing: the reset key changes immediately, and a
  // crashed component left in place throws again against the fresh boundary.
  const reload = useCallback(() => {
    setMounted(null);
    setLoadError(null);
    setAttempt((n) => n + 1);
  }, []);

  const handleFocus = useCallback(() => setHoldsFocus(true), []);
  const handleBlur = useCallback((event: ReactFocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setHoldsFocus(false);
    }
  }, []);

  const viewProps = useMemo(
    () => ({ workspaceId, sessionId, tileId, params }),
    [workspaceId, sessionId, tileId, params],
  );

  // Composed from the mount's identity, never taken from the view: an app is
  // only ever handed its own namespace.
  const runtime = useMemo<AppViewRuntime>(
    () => ({
      namespace: `app/${app}`,
      subscribe: subscribeDocuments,
      command: (command: string, payload?: unknown) => sendAppCommand(app, command, payload),
    }),
    [app, subscribeDocuments, sendAppCommand],
  );

  const body = (() => {
    if (!entry) {
      return (
        <Placeholder
          kind="not-installed"
          title={`The app “${app}” is not installed.`}
          detail={`This tile stays where you put it. Apply the app again with \`attn app apply <path>\` and it will mount, or close the tile to remove it.`}
        />
      );
    }
    if (!entry.enabled) {
      return (
        <Placeholder
          kind="disabled"
          title={`${app} is disabled.`}
          detail={`Its views do not run while it is off. \`attn app enable ${app}\` turns it back on.`}
        />
      );
    }
    if (servingVersion === 0 || servingHash === '') {
      return (
        <Placeholder
          kind="no-version"
          title={`${app} has no version serving.`}
          detail={`\`attn app apply <path>\` builds and installs one; \`attn app status ${app}\` shows what it has.`}
        />
      );
    }
    if (!declared) {
      const names = (entry.views ?? []).map((v) => v.name);
      return (
        <Placeholder
          kind="view-gone"
          title={`${app} no longer has a view called “${view}”.`}
          detail={names.length > 0
            ? `The version serving now offers: ${names.join(', ')}. Roll back with \`attn app rollback ${app}\`, or close this tile and dock one of those.`
            : `The version serving now declares no views at all. \`attn app status ${app}\` shows what it has.`}
        />
      );
    }
    if (loadError) {
      return (
        <Placeholder
          kind="load-error"
          title={loadError.message}
          detail={loadError.detail}
          action={{ label: 'Retry', onClick: reload }}
        />
      );
    }
    if (!mounted) {
      return <div className="app-tile-host-message" data-app-view-placeholder="loading">Loading {app}/{view}…</div>;
    }
    const View = mounted.component;
    return (
      <AppViewBoundary
        resetKey={`${mounted.contentHash}:${attempt}`}
        onError={reportCrash}
        fallback={(error) => (
          <Placeholder
            kind="crashed"
            title={`${app}/${view} crashed while rendering.`}
            detail={`${error.message}\n\nThe full error is recorded against this app — \`attn app logs ${app}\` has it.`}
            action={{ label: 'Reload', onClick: reload }}
          />
        )}
      >
        <AppViewRuntimeProvider value={runtime}>
          <View {...viewProps} />
        </AppViewRuntimeProvider>
      </AppViewBoundary>
    );
  })();

  return (
    // `app_view_get_state` reads these attributes: the packaged-app harness has
    // no other way to tell a mounted view from a placeholder rendering text.
    <div
      className="app-tile-host"
      data-app-view-host={`${app}/${view}`}
      data-app-view-tile={tileId}
      data-app-view-stale={stale ? '1' : '0'}
      onFocus={handleFocus}
      onBlur={handleBlur}
    >
      {stale && (
        // Static on purpose: a tile that repaints forever costs battery on a
        // machine that keeps attn open all day.
        <div className="app-tile-host-badge" role="status">
          {deferred ? 'A new version is ready — reloading when you leave this tile' : 'Reloading…'}
        </div>
      )}
      {body}
    </div>
  );
}

function Placeholder({ kind, title, detail, action }: {
  kind: string;
  title: string;
  detail: string;
  action?: { label: string; onClick: () => void };
}) {
  return (
    <div className="app-tile-host-placeholder" data-app-view-placeholder={kind}>
      <div className="app-tile-host-placeholder-title">{title}</div>
      <div className="app-tile-host-placeholder-detail">{detail}</div>
      {action && (
        <button type="button" className="app-tile-host-placeholder-action" onClick={action.onClick}>
          {action.label}
        </button>
      )}
    </div>
  );
}
