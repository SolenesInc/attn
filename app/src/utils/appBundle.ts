import { resolveDaemonHTTPOrigin } from './daemonEndpoint';

// Mirrors AppBundleURLPath in internal/daemon/app_bundle.go. The path carries the
// version's content hash, so a new version is a different URL, not a cache to bust.
export const APP_BUNDLE_ROUTE_PREFIX = '/apps/bundle/';

export const APP_VIEW_TILE_KIND_PREFIX = 'app:';

/** The browser's module map caches a module's evaluation result by URL for the life
 * of the page, so a bundle that threw would fail forever; `attempt` varies the URL. */
export function appBundleURL(app: string, contentHash: string, view: string, attempt = 0): string {
  const url = `${resolveDaemonHTTPOrigin()}${APP_BUNDLE_ROUTE_PREFIX}${app}/${contentHash}/${view}.js`;
  return attempt > 0 ? `${url}?retry=${attempt}` : url;
}

export function appViewTileKind(app: string, view: string): string {
  return `${APP_VIEW_TILE_KIND_PREFIX}${app}/${view}`;
}

export function parseAppViewTileKind(tileKind: string): { app: string; view: string } | null {
  if (!tileKind.startsWith(APP_VIEW_TILE_KIND_PREFIX)) return null;
  const rest = tileKind.slice(APP_VIEW_TILE_KIND_PREFIX.length);
  const slash = rest.indexOf('/');
  if (slash <= 0 || slash === rest.length - 1) return null;
  return { app: rest.slice(0, slash), view: rest.slice(slash + 1) };
}
