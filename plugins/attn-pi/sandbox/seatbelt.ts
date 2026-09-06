import { within } from "../security/policy";
import { seatbeltBase, seatbeltNetwork, seatbeltPreferences } from "./policies";
import type { SandboxSpec } from "./spec";

// Only /usr/bin/sandbox-exec is considered: a PATH entry could be attacker
// controlled, and tampering with /usr/bin already implies root (seatbelt.rs:63).
export const seatbeltExecutable = "/usr/bin/sandbox-exec";

type Param = { key: string; value: string };

/** Excludes the protected path itself and everything under it (seatbelt.rs:576-583). */
function exclusions(prefix: string, paths: string[], params: Param[]): string[] {
  return paths.flatMap((path, index) => {
    const key = `${prefix}_EXCLUDED_${index}`;
    params.push({ key, value: path });
    return [`(require-not (literal (param "${key}")))`, `(require-not (subpath (param "${key}")))`];
  });
}

function accessPolicy(action: string, prefix: string, roots: string[], excluded: (root: string) => string[], params: Param[]): string[] {
  const components = roots.map((root, index) => {
    const key = `${prefix}_${index}`;
    params.push({ key, value: root });
    const filter = `(subpath (param "${key}"))`;
    const parts = [filter, ...exclusions(key, excluded(root), params)];
    return parts.length === 1 ? filter : `(require-all ${parts.join(" ")})`;
  });
  if (!components.length) return [];
  return [`(allow ${action}\n${components.join(" ")}\n)`];
}

function networkPolicy(spec: SandboxSpec): string[] {
  if (!spec.network.enabled) return [];
  const proxy = spec.network.proxy;
  // With a proxy the only reachable endpoint is its loopback port, so DNS and
  // every other destination must go through it (seatbelt.rs:332-336).
  if (proxy) return [`(allow network-outbound (remote ip "localhost:${proxy.port}"))`, seatbeltNetwork];
  return ["(allow network*)", seatbeltNetwork];
}

export function seatbeltInvocation(spec: SandboxSpec): { profile: string; params: Param[] } {
  const params: Param[] = [];
  const read = accessPolicy("file-read*", "READABLE_ROOT", ["/"], () => spec.denyRead, params);
  const write = accessPolicy(
    "file-write*", "WRITABLE_ROOT", spec.writableRoots,
    (root) => spec.denyWrite.filter((path) => within(path, root) || within(root, path)), params,
  );
  // A sandboxed process must not replace a boundary the next policy reuses.
  const anchors = spec.writableRoots.map((_root, index) =>
    `(deny file-write-unlink (require-all (literal (param "WRITABLE_ROOT_${index}")) (vnode-type DIRECTORY)))`);
  const denies = [
    ...spec.denyRead.map((path, index) => {
      params.push({ key: `DENY_READ_${index}`, value: path });
      return `(deny file-read* (subpath (param "DENY_READ_${index}")))`;
    }),
    ...spec.denyWrite.map((path, index) => {
      params.push({ key: `DENY_WRITE_${index}`, value: path });
      return `(deny file-write* (subpath (param "DENY_WRITE_${index}")))`;
    }),
  ];
  // Codex also appends its platform defaults, which grant /tmp writes; attn's
  // profile grants only the temp directory it created for the session.
  const profile = [seatbeltBase, ...read, ...write, ...networkPolicy(spec), seatbeltPreferences, ...anchors, ...denies].join("\n");
  return { profile, params };
}

export function seatbeltArgs(spec: SandboxSpec, command: string[]): string[] {
  const { profile, params } = seatbeltInvocation(spec);
  return ["-p", profile, ...params.map(({ key, value }) => `-D${key}=${value}`), "--", ...command];
}
