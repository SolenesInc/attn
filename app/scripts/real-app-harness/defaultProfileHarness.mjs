import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export const DEFAULT_PROFILE_HARNESS_PACKAGING_PROFILE = 'legacy-recovery';

const ROUTING_ENV = [
  'ATTN_DATA_DIR',
  'ATTN_HARNESS_NOTEBOOK_ROOT',
  'ATTN_SOCKET_PATH',
  'ATTN_DB_PATH',
  'ATTN_CONFIG_PATH',
  'ATTN_PLUGIN_DIR',
  'ATTN_WS_PORT',
  'ATTN_CLIENT_TOKEN',
  'ATTN_WRAPPER_PATH',
  'ATTN_INSIDE_APP',
  'ATTN_DAEMON_MANAGED',
  'ATTN_PTY_WORKER',
  'ATTN_SESSION_ID',
  'ATTN_AGENT',
];

function resolvedPath(candidate) {
  let existing = path.resolve(candidate);
  const tail = [];
  while (!fs.existsSync(existing)) {
    const parent = path.dirname(existing);
    if (parent === existing) break;
    tail.unshift(path.basename(existing));
    existing = parent;
  }
  return path.join(fs.realpathSync(existing), ...tail);
}

function under(candidate, root) {
  const relative = path.relative(root, candidate);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}

export function assertDefaultProfileHarnessIsolation({
  dataDir,
  toolHome,
  codexHome,
  notebookRoot,
  appPath,
  bundleId,
  wsUrl,
  productionRoot = path.join(os.homedir(), '.attn'),
}) {
  if (!path.isAbsolute(dataDir)) throw new Error('default-profile harness data root must be absolute');
  const metadata = fs.lstatSync(dataDir);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
    throw new Error('default-profile harness data root must be a direct directory');
  }
  if ((metadata.mode & 0o077) !== 0) {
    throw new Error('default-profile harness data root must be owner-only');
  }

  const resolvedData = resolvedPath(dataDir);
  const resolvedProduction = resolvedPath(productionRoot);
  const paths = {
    dataDir: resolvedData,
    database: resolvedPath(path.join(dataDir, 'attn.db')),
    socket: resolvedPath(path.join(dataDir, 'attn.sock')),
    config: resolvedPath(path.join(dataDir, 'config.json')),
    plugins: resolvedPath(path.join(dataDir, 'plugins')),
    toolHome: resolvedPath(toolHome),
    codexHome: resolvedPath(codexHome),
    notebookRoot: resolvedPath(notebookRoot),
  };
  for (const [label, candidate] of Object.entries(paths)) {
    if (under(candidate, resolvedProduction)) {
      throw new Error(`refusing ${label} inside production ${resolvedProduction}`);
    }
    if (!under(candidate, resolvedData) && label !== 'dataDir') {
      throw new Error(`refusing ${label} outside isolated data root ${resolvedData}`);
    }
  }

  const url = new URL(wsUrl);
  if (url.hostname !== '127.0.0.1' || url.pathname !== '/ws' || ['9849', '29849'].includes(url.port)) {
    throw new Error(`refusing unsafe default-profile harness websocket ${wsUrl}`);
  }
  if (path.basename(appPath).toLowerCase() === 'attn.app' || bundleId === 'com.attn.manager') {
    throw new Error('refusing the production app bundle for the default-profile harness');
  }
  return paths;
}

export function defaultProfileHarnessEnv({ dataDir, toolHome, codexHome, notebookRoot, wsPort, clientToken = '' }) {
  const env = { ...process.env };
  for (const key of ROUTING_ENV) delete env[key];
  return {
    ...env,
    ATTN_PROFILE: '',
    ATTN_DATA_DIR: dataDir,
    ATTN_HARNESS_DATA_DIR: dataDir,
    ATTN_HARNESS_NOTEBOOK_ROOT: notebookRoot,
    ATTN_WS_PORT: String(wsPort),
    ATTN_TOOL_HOME: toolHome,
    CODEX_HOME: codexHome,
    ATTN_CLIENT_TOKEN: clientToken,
    ATTN_AUTOMATION: '1',
    ATTN_AUTOMATION_TICKET_RETENTION_TTL: '1s',
    ATTN_AUTOMATION_TICKET_RETENTION_SWEEP_INTERVAL: '200ms',
  };
}
