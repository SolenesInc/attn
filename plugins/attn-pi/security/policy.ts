import { existsSync, mkdirSync, readFileSync, realpathSync, renameSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { cachePath, defaultBuildCaches, prepareBuildCaches, type BuildCaches } from "./caches";
import { writeRecovery } from "./guidance";

export type SecurityConfig = {
  enabled: boolean;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  network: "allow" | "deny";
  buildCaches: BuildCaches;
};

export type SecurityPolicy = SecurityConfig & { cwd: string; temp: string; configPath: string; cacheWritePaths: string[]; unavailableCaches: string[] };

export function canonical(path: string): string {
  const absolute = resolve(path);
  try {
    return realpathSync(absolute);
  } catch (error) {
    if (!["ENOENT", "ENOTDIR"].includes((error as NodeJS.ErrnoException).code ?? "")) throw error;
    const parent = dirname(absolute);
    if (parent === absolute) return absolute;
    return join(canonical(parent), absolute.slice(parent.length));
  }
}

export function within(path: string, root: string): boolean {
  const rest = relative(root, path);
  return rest === "" || (rest !== ".." && !rest.startsWith("../") && !isAbsolute(rest));
}

export function expand(path: string, base: string): string {
  const expanded = path === "~" ? homedir() : path.startsWith("~/") ? join(homedir(), path.slice(2)) : path;
  return canonical(resolve(base, expanded));
}

export function loadSecurityConfig(configPath: string): SecurityConfig {
  const defaults: SecurityConfig = {
    enabled: true,
    allowWrite: [],
    denyRead: ["~/.ssh", "~/.aws", "~/.gnupg", "~/.git-credentials", "~/.netrc", "~/.config/gcloud", dirname(configPath)],
    denyWrite: [dirname(configPath)],
    network: "allow",
    buildCaches: defaultBuildCaches(),
  };
  if (!existsSync(configPath)) return defaults;
  const raw = JSON.parse(readFileSync(configPath, "utf8"));
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("Security settings must be an object");
  const config = { ...defaults, ...raw, buildCaches: { ...defaults.buildCaches, ...raw.buildCaches } };
  if (raw.buildCaches !== undefined && (!raw.buildCaches || typeof raw.buildCaches !== "object" || Array.isArray(raw.buildCaches))) throw new Error("Invalid security settings: buildCaches");
  if (!config.buildCaches || typeof config.buildCaches.enabled !== "boolean" || !Array.isArray(config.buildCaches.paths) ||
      config.buildCaches.paths.some((value: unknown) => typeof value !== "string" || !value.trim())) throw new Error("Invalid security settings: buildCaches");
  config.buildCaches.paths.forEach(cachePath);
  if (typeof config.enabled !== "boolean" || !["allow", "deny"].includes(config.network)) throw new Error("Invalid security settings: enabled/network");
  for (const field of ["allowWrite", "denyRead", "denyWrite"] as const) {
    if (!Array.isArray(config[field]) || config[field].some((value: unknown) => typeof value !== "string" || !value.trim())) throw new Error(`Invalid security settings: ${field}`);
  }
  return config;
}

export function saveSecurityConfig(path: string, config: SecurityConfig): void {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  const temporary = `${path}.${process.pid}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
  renameSync(temporary, path);
}

export function resolveSecurityPolicy(config: SecurityConfig, cwd: string, configPath: string, temp: string): SecurityPolicy {
  cwd = canonical(cwd);
  // Bubblewrap needs existing mount points; empty control directories stay outside Git's index.
  if (config.enabled && process.platform === "linux") {
    for (const name of [".pi", ".agents"]) {
      try { mkdirSync(join(cwd, name), { mode: 0o700 }); }
      catch (error) { if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error; }
    }
  }
  const denyRead = config.denyRead.map((path) => expand(path, homedir()));
  const denyWrite = [...new Set([...config.denyWrite.map((path) => expand(path, homedir())), canonical(configPath), canonical(join(cwd, ".pi")), canonical(join(cwd, ".agents"))])];
  const caches = prepareBuildCaches({ ...config.buildCaches, enabled: config.enabled && config.buildCaches.enabled }, [...denyRead, ...denyWrite]);
  const roots = [cwd, temp, ...config.allowWrite.map((path) => expand(path, homedir())), ...gitWritePaths(cwd), ...caches.paths];
  if (roots.some((path) => path === parse(path).root)) throw new Error("A filesystem root cannot be a sandbox write grant");
  return {
    ...config, cwd, temp: canonical(temp), configPath,
    allowWrite: [...new Set(roots.map(canonical))],
    denyRead, denyWrite, cacheWritePaths: caches.paths, unavailableCaches: caches.unavailable,
  };
}

function gitWritePaths(cwd: string): string[] {
  for (let directory = cwd; ; directory = dirname(directory)) {
    const marker = join(directory, ".git");
    try {
      const text = readFileSync(marker, "utf8");
      const match = /^gitdir: (.+)\s*$/m.exec(text);
      if (match) {
        const git = expand(match[1]!.trim(), directory);
        const commonFile = join(git, "commondir");
        if (!existsSync(commonFile) || !existsSync(join(git, "gitdir"))) return [];
        const common = expand(readFileSync(commonFile, "utf8").trim(), git);
        // A project-controlled gitdir pointer alone must never grant an arbitrary directory.
        if (dirname(git) !== join(common, "worktrees") || expand(readFileSync(join(git, "gitdir"), "utf8").trim(), git) !== canonical(marker)) return [];
        if (!["HEAD", "objects", "refs"].every((name) => existsSync(join(common, name)))) return [];
        return [git, common];
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "EISDIR") return canonical(marker) === marker ? [marker] : [];
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    if (dirname(directory) === directory) return [];
  }
}

export function assertPath(policy: SecurityPolicy, path: string, mode: "read" | "write", reviewAvailable = false): string {
  const target = expand(path.replace(/^@/, ""), policy.cwd);
  if (!policy.enabled) return target;
  const denies = mode === "write" ? policy.denyWrite : policy.denyRead;
  const protectedBy = denies.find((root) => within(target, canonical(root)));
  if (protectedBy) throw new Error(`Security blocked ${mode}: ${target}. This path is explicitly protected by ${protectedBy}. Auto mode cannot override this restriction. Explain what you need to the user; do not retry through another tool or change security settings yourself.`);
  if (mode === "write" && !policy.allowWrite.some((root) => within(target, root))) {
    throw new Error(`Security blocked write outside allowed paths: ${target}. ${writeRecovery(policy, reviewAvailable)}`);
  }
  return target;
}
