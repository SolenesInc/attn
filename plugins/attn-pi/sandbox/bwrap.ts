import { accessSync, constants, existsSync, statSync } from "node:fs";
import { delimiter, dirname, isAbsolute, join } from "node:path";
import { canonical, within } from "../security/policy";
import { outermostRoots, type SandboxSpec } from "./spec";

type Mount = { path: string; access: "write" | "read" | "hidden" };

function existingParent(path: string): string {
  if (existsSync(path)) return path;
  const parent = dirname(path);
  if (parent === path) throw new Error(`Cannot protect missing sandbox path: ${path}`);
  return existingParent(parent);
}

function mounts(spec: SandboxSpec): Mount[] {
  // Nested roots matter to the Seatbelt anchors, not to a bind mount.
  const writable = outermostRoots(spec.writableRoots);
  for (const path of writable) {
    if (!existsSync(path)) throw new Error(`Sandbox write grant does not exist: ${path}`);
  }
  const readOnly = outermostRoots(spec.denyWrite.map(existingParent)).filter((path) =>
    writable.some((grant) => within(path, grant) || within(grant, path)));
  const hidden = outermostRoots(spec.denyRead.filter(existsSync));
  return [
    ...writable.map((path): Mount => ({ path, access: "write" })),
    ...readOnly.map((path): Mount => ({ path, access: "read" })),
    ...hidden.map((path): Mount => ({ path, access: "hidden" })),
  ];
}

// Flags follow the restricted builder, linux-sandbox/src/bwrap.rs:454-466 and
// :329-352. `--dev` gives a private /dev/shm; a writable root rebinds the host's.
export function bwrapArgs(spec: SandboxSpec, command: string[]): string[] {
  const args = ["--new-session", "--die-with-parent", "--ro-bind", "/", "/", "--dev", "/dev"];
  for (const mount of mounts(spec)) {
    switch (mount.access) {
      case "write": args.push("--bind", mount.path, mount.path); break;
      case "read": args.push("--ro-bind", mount.path, mount.path); break;
      case "hidden":
        if (statSync(mount.path).isDirectory()) args.push("--tmpfs", mount.path, "--remount-ro", mount.path);
        else args.push("--ro-bind", "/dev/null", mount.path);
        break;
    }
  }
  args.push("--unshare-user", "--unshare-pid", "--unshare-ipc");
  // Proxy routing (linux-sandbox/src/proxy_routing.rs) needs a helper binary
  // inside the namespace; until attn has one, network on means a shared netns.
  if (!spec.network.enabled) args.push("--unshare-net");
  args.push("--proc", "/proc", "--chdir", spec.cwd, "--cap-drop", "ALL", "--", ...command);
  return args;
}

export function bwrapExecutable(spec: SandboxSpec): string {
  const directories = (process.env.PATH ?? "").split(delimiter).filter(isAbsolute);
  for (const directory of directories) {
    try {
      const binary = canonical(join(directory, "bwrap"));
      if (spec.writableRoots.some((grant) => within(binary, grant)) || !statSync(binary).isFile()) continue;
      accessSync(binary, constants.X_OK);
      return binary;
    } catch { /* Continue searching when a PATH entry is absent or inaccessible. */ }
  }
  throw new Error("Install bubblewrap (bwrap) on PATH outside the sandbox write grants");
}
