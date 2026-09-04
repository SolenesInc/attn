import { afterEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { PiSecurity } from "../security/index";
import { canonical, loadSecurityConfig, resolveSecurityPolicy } from "../security/policy";
import { defaultBuildCaches } from "../security/caches";
import { protectedBash } from "../security/tools";
import { CredentialFilter } from "../security/filter";
import { shellQuote } from "../security/sandbox";

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => { for (const close of cleanups.splice(0)) await close(); });

function fixture() {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-cache-test-")));
  for (const name of ["project", "agent", "temp", "private"]) mkdirSync(join(root, name));
  const configPath = join(root, "agent", "attn-security.json");
  const config = loadSecurityConfig(configPath);
  config.buildCaches.paths = [join(root, "caches", "build")];
  config.denyRead = [join(root, "private")];
  config.denyWrite = [join(root, "private"), join(root, "agent")];
  const save = () => writeFileSync(configPath, JSON.stringify(config));
  save();
  cleanups.push(async () => rmSync(root, { recursive: true, force: true }));
  const resolve = () => resolveSecurityPolicy(config, join(root, "project"), configPath, join(root, "temp"));
  return { root, configPath, config, save, resolve };
}

test("existing settings acquire configurable cache defaults; disabling needs no path list", () => {
  const { configPath } = fixture();
  writeFileSync(configPath, JSON.stringify({ enabled: true }));
  expect(loadSecurityConfig(configPath).buildCaches).toEqual(defaultBuildCaches());
  writeFileSync(configPath, JSON.stringify({ buildCaches: { enabled: false } }));
  expect(loadSecurityConfig(configPath).buildCaches.enabled).toBe(false);
  writeFileSync(configPath, JSON.stringify({ buildCaches: { paths: [] } }));
  expect(loadSecurityConfig(configPath).buildCaches.paths).toEqual([]);
  expect(defaultBuildCaches("linux").paths).toContain("~/.cache/go-build");
  expect(defaultBuildCaches("darwin").paths).toContain("~/Library/Caches/go-build");
});

test("cache setup creates only the requested directories and preserves explicit denies", async () => {
  const { root, config, resolve } = fixture();
  config.buildCaches.paths.push(join(root, "private", "cache"));
  const policy = resolve();
  expect(policy.cacheWritePaths).toEqual([join(root, "caches", "build")]);
  expect(policy.unavailableCaches.join()).toContain("explicitly protected");
  expect(existsSync(join(root, "private", "cache"))).toBe(false);
  const execute = (command: string) => protectedBash(policy, new CredentialFilter()).exec(command, policy.cwd, { onData() {} });
  expect((await execute(`mkdir -p ${shellQuote(join(root, "caches", "build", "locks"))} && echo compiled > ${shellQuote(join(root, "caches", "build", "locks", "artifact"))}`)).exitCode).toBe(0);
  expect((await execute(`echo forbidden > ${shellQuote(join(root, "caches", "sibling"))}`)).exitCode).not.toBe(0);
  expect(existsSync(join(root, "caches", "sibling"))).toBe(false);
});

test("cache symlinks cannot silently grant another tree or create protected directories", () => {
  const { root, config, resolve } = fixture();
  symlinkSync(join(root, "private"), join(root, "redirect"));
  config.buildCaches.paths = [join(root, "redirect", "new-cache")];
  const policy = resolve();
  expect(policy.cacheWritePaths).toEqual([]);
  expect(policy.unavailableCaches.join()).toContain("symlink");
  expect(existsSync(join(root, "private", "new-cache"))).toBe(false);
});

test("root, home and malformed cache settings fail closed", () => {
  const { configPath, config } = fixture();
  for (const path of ["/", homedir(), "~", "relative/cache"]) {
    writeFileSync(configPath, JSON.stringify({ ...config, buildCaches: { enabled: true, paths: [path] } }));
    expect(() => loadSecurityConfig(configPath)).toThrow();
  }
  for (const buildCaches of [null, false, [], { enabled: "yes" }, { paths: [7] }]) {
    writeFileSync(configPath, JSON.stringify({ ...config, buildCaches }));
    expect(() => loadSecurityConfig(configPath)).toThrow("buildCaches");
  }
});

test("cache controls and permission prompts take effect in the same session", async () => {
  const { root, configPath, config } = fixture();
  const handlers = new Map<string, any>();
  const commands = new Map<string, any>();
  const tools = new Map<string, any>();
  const notices: string[] = [];
  const ctx = { cwd: join(root, "project"), ui: { setStatus() {}, notify: (text: string) => notices.push(text) } };
  let available = true;
  const security = new PiSecurity(configPath, async () => undefined, () => available);
  security.register({ on: (name: string, handler: any) => handlers.set(name, handler), registerCommand: (name: string, command: any) => commands.set(name, command), registerTool: (tool: any) => tools.set(tool.name, tool) } as never);
  cleanups.unshift(async () => handlers.get("session_shutdown")({}, ctx));
  await handlers.get("session_start")({}, ctx);
  const command = (text: string) => commands.get("security").handler(text, ctx);
  const prompt = () => handlers.get("before_agent_start")({ systemPrompt: "base" }).systemPrompt;
  const cache = config.buildCaches.paths[0]!;
  const run = (path: string) => tools.get("write").execute("cache-write", { path, content: "compiled" }, undefined, undefined, ctx);
  await run(join(cache, "first"));
  expect(prompt()).toContain('Auto-mode access review: available');
  expect(prompt()).toContain(cache);
  available = false;
  expect(prompt()).toContain('Auto-mode access review: unavailable');
  await command("caches off");
  expect(security.cacheWritePaths()).toEqual([]);
  await expect(run(join(cache, "disabled"))).rejects.toThrow("auto mode is off");
  expect(prompt()).toContain("Build-cache grants: disabled");
  expect(existsSync(join(cache, "first"))).toBe(true);
  await command(`allow-write ${cache}`);
  await run(join(cache, "explicit"));
  await command(`revoke-write ${cache}`);
  await command("caches on");
  await run(join(cache, "enabled"));
  await command(`caches remove ${cache}`);
  await expect(run(join(cache, "removed"))).rejects.toThrow("outside allowed");
  const custom = join(root, "custom", "cache");
  await command(`caches add ${custom}`);
  await run(join(custom, "works"));
  expect(JSON.parse(readFileSync(configPath, "utf8")).buildCaches.paths).toEqual([custom]);
  await command("status");
  expect(notices.at(-1)).toContain(`Active cache grants: ${custom}`);
  await command("off");
  expect(prompt()).toContain("Omit bash.sandbox");
  expect(prompt()).toContain("Tool network: unrestricted (sandbox disabled)");
  expect(prompt()).not.toContain("retry bash");
});

test("network failures get policy-aware recovery without mislabeling unrelated failures", async () => {
  const policy = fixture().resolve();
  const failure = "printf 'getaddrinfo ENOTFOUND registry.example.invalid' >&2; exit 1";
  const run = async (network: "allow" | "deny", available: boolean) => {
    let output = "";
    await protectedBash({ ...policy, network }, new CredentialFilter(), () => available).exec(failure, policy.cwd, { onData: (part) => { output += part; } });
    return output;
  };
  expect(await run("deny", true)).toContain('sandbox: {network: "allow"');
  expect(await run("deny", false)).toContain("auto mode is off");
  expect(await run("allow", true)).not.toContain("sandbox:");
});
