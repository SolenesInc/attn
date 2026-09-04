import { accessSync, constants, existsSync, statSync } from "node:fs";
import { delimiter, dirname, isAbsolute, join } from "node:path";
import { canonical, within, type SecurityPolicy } from "./policy";
import { seatbeltBase, seatbeltNetwork } from "./seatbelt-base";

export type SandboxedCommand = { executable: string; args: string[] };

export function shellQuote(value: string): string {
  return "'" + value.split("'").join("'\"'\"'") + "'";
}

export function seatbeltProfile(policy: SecurityPolicy): string {
  const subtree = (path: string) => `(subpath ${JSON.stringify(path)})`;
  const readExclusions = policy.denyRead.map((path) => `(require-not ${subtree(path)})`);
  const reads = readExclusions.length ? `(require-all ${readExclusions.join(" ")})` : "";
  const writes = policy.allowWrite.map(subtree).join(" ");
  const restrictions = [
    ...policy.denyRead.map((path) => `(deny file-read* ${subtree(path)})`),
    ...policy.denyWrite.map((path) => `(deny file-write* ${subtree(path)})`),
  ];
  return [
    seatbeltBase,
    `(allow file-read* ${reads})`,
    ...(writes ? [`(allow file-write* ${writes})`] : []),
    ...(policy.network === "allow" ? [seatbeltNetwork, "(allow network*)"] : []),
    ...restrictions,
  ].join("\n");
}

type Mount = { path: string; access: "write" | "read" | "hidden" };

function roots(paths: string[]): string[] {
  const ordered = [...new Set(paths.map(canonical))].sort((left, right) => left.length - right.length);
  return ordered.filter((path, index) => !ordered.slice(0, index).some((parent) => within(path, parent)));
}

function existingParent(path: string): string {
  if (existsSync(path)) return path;
  const parent = dirname(path);
  if (parent === path) throw new Error(`Cannot protect missing sandbox path: ${path}`);
  return existingParent(parent);
}

function linuxMounts(policy: SecurityPolicy): Mount[] {
  const writable = roots(policy.allowWrite);
  for (const path of writable) {
    if (!existsSync(path)) throw new Error(`Sandbox write grant does not exist: ${path}`);
  }
  const readOnly = roots(policy.denyWrite.map(existingParent)).filter((path) =>
    writable.some((grant) => within(path, grant) || within(grant, path)));
  const hidden = roots(policy.denyRead.filter(existsSync));
  return [
    ...writable.map((path): Mount => ({ path, access: "write" })),
    ...readOnly.map((path): Mount => ({ path, access: "read" })),
    ...hidden.map((path): Mount => ({ path, access: "hidden" })),
  ];
}

// Mount operations and namespace flags follow bubblewrap's bwrap(1) interface.
// Apply restrictive mounts last so a writable parent cannot reopen a protected path.
function linuxCommand(policy: SecurityPolicy, executable: string, args: string[]): SandboxedCommand {
  const options = ["--die-with-parent", "--new-session", "--unshare-all"];
  if (policy.network === "allow") options.push("--share-net");
  options.push("--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev");
  for (const mount of linuxMounts(policy)) {
    switch (mount.access) {
      case "write": options.push("--bind", mount.path, mount.path); break;
      case "read": options.push("--ro-bind", mount.path, mount.path); break;
      case "hidden":
        if (statSync(mount.path).isDirectory()) options.push("--tmpfs", mount.path, "--remount-ro", mount.path);
        else options.push("--ro-bind", "/dev/null", mount.path);
        break;
    }
  }
  return { executable: bubblewrapExecutable(policy), args: [...options, "--chdir", policy.cwd, "--", executable, ...args] };
}

function bubblewrapExecutable(policy: SecurityPolicy): string {
  const directories = (process.env.PATH ?? "").split(delimiter).filter(isAbsolute);
  for (const directory of directories) {
    try {
      const binary = canonical(join(directory, "bwrap"));
      if (policy.allowWrite.some((grant) => within(binary, grant)) || !statSync(binary).isFile()) continue;
      accessSync(binary, constants.X_OK);
      return binary;
    } catch { /* Continue searching when a PATH entry is absent or inaccessible. */ }
  }
  throw new Error("Install bubblewrap (bwrap) on PATH outside the sandbox write grants");
}

export function sandboxCommand(policy: SecurityPolicy, executable: string, args: string[], platform = process.platform): SandboxedCommand {
  if (!policy.enabled) return { executable, args };
  switch (platform) {
    case "darwin": return { executable: "/usr/bin/sandbox-exec", args: ["-p", seatbeltProfile(policy), executable, ...args] };
    case "linux": return linuxCommand(policy, executable, args);
    default: throw new Error(`Pi security sandbox is unavailable on ${platform}`);
  }
}

export function bashSandbox(policy: SecurityPolicy, command: string): string {
  const invocation = sandboxCommand(policy, "/bin/bash", ["--noprofile", "--norc", "-c", command]);
  return [invocation.executable, ...invocation.args].map(shellQuote).join(" ");
}
