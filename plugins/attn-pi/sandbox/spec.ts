import { statSync } from "node:fs";
import { canonical, within } from "../security/policy";

export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";
export type SandboxPermissions = "use_default" | "require_escalated";
/** credentials = the run's proxy credentials (ATTN_PI_PROXY_CREDENTIALS), never the run token. */
export type ProxyAddress = { host: "127.0.0.1"; port: number; credentials: string };

export type SandboxSpec = {
  mode: SandboxMode;
  cwd: string;
  writableRoots: string[];
  denyRead: string[];
  denyWrite: string[];
  temp: string;
  network: { enabled: boolean; proxy?: ProxyAddress };
};

export type SandboxConfig = {
  mode: SandboxMode;
  network: boolean;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  cacheWritePaths: string[];
};

/** Drops duplicates and paths already covered by a shorter root. */
export function outermostRoots(paths: string[]): string[] {
  const ordered = [...new Set(paths.map(canonical))].sort((left, right) => left.length - right.length);
  return ordered.filter((path, index) => !ordered.slice(0, index).some((parent) => within(path, parent)));
}

// protocol.rs:1275-1286 grants /tmp on unix when it is a directory; /dev/shm is
// how a workspace gets POSIX semaphores (exec/tests/suite/sandbox.rs:166-171).
function platformWritableRoots(): string[] {
  if (process.platform === "win32") return [];
  return ["/tmp", "/dev/shm"].filter((path) => {
    try { return statSync(path).isDirectory(); } catch { return false; }
  });
}

export function sandboxSpecFor(
  config: SandboxConfig,
  cwd: string,
  temp: string,
  opts: { permissions: SandboxPermissions; proxy?: ProxyAddress },
): SandboxSpec | "unsandboxed" {
  // config_toml.rs:809 maps danger-full-access to PermissionProfile::Disabled.
  if (config.mode === "danger-full-access") return "unsandboxed";
  // Codex refuses this while denied reads exist (core/src/tools/sandboxing.rs:275-283).
  // attn asks a reviewer first, and that approval covers escaping the deny list too.
  if (opts.permissions === "require_escalated") return "unsandboxed";
  const resolvedCwd = canonical(cwd);
  const resolvedTemp = canonical(temp);
  // Every configured root stays its own root, nested or not: each one carries an
  // anchor deny, and collapsing a nested root would drop that boundary.
  const writableRoots = config.mode === "workspace-write"
    ? [...new Set([resolvedCwd, resolvedTemp, ...platformWritableRoots(), ...config.allowWrite, ...config.cacheWritePaths].map(canonical))]
    : [];
  const proxy = config.network ? opts.proxy : undefined;
  return {
    mode: config.mode,
    cwd: resolvedCwd,
    temp: resolvedTemp,
    writableRoots,
    denyRead: outermostRoots(config.denyRead),
    denyWrite: [...new Set(config.denyWrite.map(canonical))],
    network: { enabled: config.network, ...(proxy ? { proxy } : {}) },
  };
}

export function proxyUrl(proxy: ProxyAddress, scheme: "http" | "socks5h"): string {
  return `${scheme}://${encodeURIComponent(proxy.credentials)}@${proxy.host}:${proxy.port}`;
}
