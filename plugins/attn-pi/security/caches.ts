import { mkdirSync, statSync } from "node:fs";
import { homedir, userInfo } from "node:os";
import { isAbsolute, join, parse, resolve } from "node:path";
import { canonical, within } from "./policy";

export type BuildCaches = { enabled: boolean; paths: string[] };

export function defaultBuildCaches(platform = process.platform): BuildCaches {
  const cache = platform === "darwin" ? "~/Library/Caches" : "~/.cache";
  return { enabled: true, paths: [
    `${cache}/go-build`, "~/go/pkg/mod", "~/.npm", "~/.bun/install/cache",
    `${cache}/pip`, "~/.cache/uv", `${cache}/${platform === "darwin" ? "Yarn" : "yarn"}`, `${cache}/pnpm`,
    platform === "darwin" ? "~/Library/pnpm/store" : "~/.local/share/pnpm/store",
    "~/.cache/node/corepack", `${cache}/bazel`, `${cache}/bazelisk`,
    "~/.cache/bazel_repo", "~/.cache/bazel_disk",
    ...(platform === "darwin" ? [join("/private/var/tmp", `_bazel_${userInfo().username}`)] : []),
    "~/.m2/repository", "~/.gradle/caches", "~/.gradle/daemon", "~/.gradle/native", "~/.gradle/wrapper/dists",
  ] };
}

export function cachePath(value: string): string {
  if (!isAbsolute(value) && !value.startsWith("~/")) throw new Error("Build cache paths must be absolute or start with ~/");
  const path = resolve(value.startsWith("~/") ? join(canonical(homedir()), value.slice(2)) : value);
  if (path === parse(path).root || path === canonical(homedir())) throw new Error("Name a specific build cache, not the filesystem root or home directory");
  return path;
}

export function prepareBuildCaches(config: BuildCaches, denied: string[]): { paths: string[]; unavailable: string[] } {
  const paths: string[] = [];
  const unavailable: string[] = [];
  if (!config.enabled) return { paths, unavailable };
  for (const value of config.paths) {
    const path = cachePath(value);
    try {
      // Never let a pre-existing symlink turn a preset into a grant for a different tree.
      if (canonical(path) !== path) throw new Error("resolves through a symlink; configure the real cache path explicitly");
      if (denied.some((root) => within(path, root))) throw new Error("explicitly protected by the security policy");
      mkdirSync(path, { recursive: true, mode: 0o700 });
      if (canonical(path) !== path || !statSync(path).isDirectory()) throw new Error("cache directory changed during setup");
      paths.push(path);
    } catch (error) {
      unavailable.push(`${path}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  return { paths: [...new Set(paths)], unavailable };
}
