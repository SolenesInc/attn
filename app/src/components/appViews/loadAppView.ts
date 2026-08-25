import type { ComponentType } from 'react';

/** What a view is given. Mirrors ViewProps in sdk/attn-app/src/index.ts. */
export interface AppViewProps {
  workspaceId: string;
  sessionId: string | null;
  tileId: string;
  params: string;
}

export type AppViewComponent = ComponentType<AppViewProps>;

// 10s, borrowed from A4's appRuntimeConnectWait — far past anything a local
// bundle read does. The tripwire is the fetch, not the network.
export const APP_VIEW_LOAD_TIMEOUT_MS = 10_000;

export class AppViewLoadError extends Error {
  readonly detail: string;
  constructor(message: string, detail: string) {
    super(message);
    this.name = 'AppViewLoadError';
    this.detail = detail;
  }
}

/** A plain dynamic import on purpose: only a real import resolves the module's
 * bare SDK specifiers against index.html's import map. */
export async function loadAppView(url: string, timeoutMs = APP_VIEW_LOAD_TIMEOUT_MS): Promise<AppViewComponent> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const module = await Promise.race([
    import(/* @vite-ignore */ url).catch((error: unknown) => {
      throw new AppViewLoadError(
        'This view could not be loaded.',
        `Importing ${url} failed: ${errorText(error)}`,
      );
    }),
    new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        reject(new AppViewLoadError(
          'This view did not load.',
          `${url} did not answer within ${timeoutMs / 1000}s. The daemon serving it may be down — check \`attn daemon status\`.`,
        ));
      }, timeoutMs);
    }),
  ]).finally(() => {
    if (timer) clearTimeout(timer);
  });

  const component = (module as { default?: unknown }).default;
  if (typeof component !== 'function') {
    const exported = Object.keys(module as object).filter((k) => k !== 'default');
    throw new AppViewLoadError(
      'This view exports no component.',
      `${url} must export a React component as its default export. `
      + (exported.length > 0 ? `It exports: ${exported.join(', ')}.` : 'It exports nothing.'),
    );
  }
  return component as AppViewComponent;
}

// WebKit's `stack` is frames only — no message line, unlike V8 — so a report built
// from the stack alone never names what was thrown.
export function errorText(error: unknown): string {
  if (!(error instanceof Error)) return String(error);
  const headline = `${error.name}: ${error.message}`;
  const stack = error.stack ?? '';
  if (!stack) return headline;
  return stack.startsWith(error.name) ? stack : `${headline}\n${stack}`;
}
