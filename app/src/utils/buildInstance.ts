/** Compile-time instance baked into this bundle ("" = production), mirroring the Rust
 * shell's ATTN_BUILD_INSTANCE. A daemon reporting a different instance is refused. */
export const BUILD_INSTANCE: string = (import.meta.env.VITE_ATTN_BUILD_INSTANCE ?? '').trim();

export const BUILD_INSTANCE_LABEL: string = BUILD_INSTANCE === '' ? 'default' : BUILD_INSTANCE;

/** Whether the daemon's reported instance matches this build; empty means "default". */
export function daemonInstanceMatches(reportedInstance: string | null | undefined): boolean {
  const reported = (reportedInstance ?? '').trim() || 'default';
  return reported === BUILD_INSTANCE_LABEL;
}

/** ws://127.0.0.1:29849/ws → http://127.0.0.1:29849/health */
export function healthURLFromWS(wsUrl: string): string {
  try {
    const u = new URL(wsUrl);
    u.protocol = u.protocol === 'wss:' ? 'https:' : 'http:';
    u.pathname = '/health';
    u.search = '';
    u.hash = '';
    return u.toString();
  } catch {
    return '';
  }
}

export interface DaemonHealthInstance {
  instance?: string;
  data_dir?: string;
  socket_path?: string;
  port?: string;
}

/** Fetches /health for the instance-identity subset. Throws on network/HTTP errors so the
 * caller decides whether no answer is a mismatch or transient. */
export async function fetchDaemonHealthInstance(wsUrl: string, signal?: AbortSignal): Promise<DaemonHealthInstance> {
  const url = healthURLFromWS(wsUrl);
  if (!url) throw new Error('cannot derive health URL from ws URL');
  const resp = await fetch(url, { signal, cache: 'no-store' });
  if (!resp.ok) throw new Error(`/health returned ${resp.status}`);
  const body = await resp.json();
  return {
    instance: typeof body?.instance === 'string' ? body.instance : undefined,
    data_dir: typeof body?.data_dir === 'string' ? body.data_dir : undefined,
    socket_path: typeof body?.socket_path === 'string' ? body.socket_path : undefined,
    port: typeof body?.port === 'string' ? body.port : undefined,
  };
}

/** Mismatch banner text; the caller shows it non-dismissably and stops reconnecting. */
export function instanceMismatchMessage(reported: string | null | undefined): string {
  const reportedLabel = (reported ?? '').trim() || 'default';
  return (
    `Instance mismatch: this app was built for instance "${BUILD_INSTANCE_LABEL}" ` +
    `but the daemon reports instance "${reportedLabel}". ` +
    `Refusing to operate on a mismatched daemon. ` +
    `Quit this app and launch the matching one (prod = attn.app, dev = attn-dev.app), ` +
    `or restart the daemon under the correct ATTN_INSTANCE.`
  );
}
