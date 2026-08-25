export interface DaemonEndpointProfile {
  id?: string;
  wsUrl?: string;
}

interface ResolveDaemonWebSocketURLOptions {
  wsUrl?: string;
  endpoint?: DaemonEndpointProfile;
}

const DEFAULT_DAEMON_WS_PROTOCOL = 'ws';
const DEFAULT_DAEMON_HOST = '127.0.0.1';
const DEFAULT_DAEMON_PORT = '9849';
const DEFAULT_DAEMON_WS_PATH = '/ws';

function normalizeWebSocketPath(path: string): string {
  const trimmed = path.trim();
  if (trimmed === '') {
    return DEFAULT_DAEMON_WS_PATH;
  }
  return trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
}

function trimOrEmpty(value?: string): string {
  return value?.trim() || '';
}

// Frontend-local: a daemon-delivered setting cannot be what discovers the
// websocket that fetches settings.
export function resolveDaemonWebSocketURL(options: ResolveDaemonWebSocketURLOptions = {}): string {
  const explicit = trimOrEmpty(options.endpoint?.wsUrl) || trimOrEmpty(options.wsUrl) || trimOrEmpty(import.meta.env.VITE_DAEMON_WS_URL);
  if (explicit !== '') {
    return explicit;
  }

  const protocol = trimOrEmpty(import.meta.env.VITE_DAEMON_WS_PROTOCOL) || DEFAULT_DAEMON_WS_PROTOCOL;
  const host = trimOrEmpty(import.meta.env.VITE_DAEMON_HOST) || DEFAULT_DAEMON_HOST;
  const port = trimOrEmpty(import.meta.env.VITE_DAEMON_PORT) || DEFAULT_DAEMON_PORT;
  const path = normalizeWebSocketPath(import.meta.env.VITE_DAEMON_WS_PATH || DEFAULT_DAEMON_WS_PATH);

  return `${protocol}://${host}:${port}${path}`;
}

// Derived from the WebSocket URL rather than resolved again: a profile that moved
// its port must move both, or a docked app view imports another profile's artifact.
export function resolveDaemonHTTPOrigin(options: ResolveDaemonWebSocketURLOptions = {}): string {
  const ws = new URL(resolveDaemonWebSocketURL(options));
  return `${ws.protocol === 'wss:' ? 'https:' : 'http:'}//${ws.host}`;
}
