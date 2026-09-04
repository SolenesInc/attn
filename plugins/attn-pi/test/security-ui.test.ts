import { afterEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { Theme } from "@earendil-works/pi-coding-agent";
import { visibleWidth } from "@earendil-works/pi-tui";
import { PiSecurity } from "../security/index";
import { canonical, loadSecurityConfig } from "../security/policy";
import { SecurityPanel } from "../security/ui";
import { defaultBuildCaches } from "../security/caches";

const theme = { fg: (_color: string, text: string) => text, bold: (text: string) => text } as Theme;
const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => { for (const cleanup of cleanups.splice(0)) await cleanup(); });

async function fixture(mode = "tui") {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-security-ui-")));
  for (const name of ["agent", "project", "cache", "outside"]) mkdirSync(join(root, name));
  const configPath = join(root, "agent", "attn-security.json");
  const config = loadSecurityConfig(configPath);
  config.buildCaches.paths = [join(root, "cache")];
  writeFileSync(configPath, JSON.stringify(config));
  const handlers = new Map<string, any>();
  const commands = new Map<string, any>();
  const tools = new Map<string, any>();
  const notices: string[] = [];
  let panel: SecurityPanel;
  let closed = false;
  let rows = 40;
  let available = true;
  const ctx = { mode, hasUI: true, cwd: join(root, "project"), ui: {
    setStatus() {}, notify: (text: string) => notices.push(text),
    custom: (make: any) => new Promise<void>((resolve) => {
      panel = make({ terminal: { get rows() { return rows; } }, requestRender() {} }, theme, {}, () => { closed = true; resolve(); });
    }),
  } };
  const security = new PiSecurity(configPath, async () => undefined, () => available);
  security.register({ on: (name: string, handler: any) => handlers.set(name, handler), registerCommand: (name: string, command: any) => commands.set(name, command), registerTool: (tool: any) => tools.set(tool.name, tool) } as never);
  cleanups.push(async () => { await handlers.get("session_shutdown")({}, ctx); rmSync(root, { recursive: true, force: true }); });
  await handlers.get("session_start")({}, ctx);
  const command = (text: string) => commands.get("security").handler(text, ctx);
  let opening = command("");
  const press = (key: string) => panel.handleInput(key);
  const screen = (width = 100) => panel.render(width).join("\n");
  const select = async (label: string) => {
    for (let i = 0; i < 100; i++) {
      if (screen().split("\n").some((line) => label.startsWith("/")
        ? line.trimStart().startsWith("→ ") && (line.endsWith(`/${basename(label)}`) || line.endsWith(`/${basename(label)} (built-in)`))
        : line.trimStart().startsWith(`→ ${label}`))) return;
      await press("\x1b[B");
    }
    throw new Error(`Cannot select ${label}: ${screen()}`);
  };
  const choose = async (label: string) => { await select(label); await press("\r"); };
  const run = (tool: string, args: unknown) => tools.get(tool).execute("ui-test", args, undefined, undefined, ctx);
  return { root, configPath, screen, press, choose, select, notices, command, run, config: () => loadSecurityConfig(configPath),
    closed: () => closed, resize: (height: number) => { rows = height; },
    reopen: () => { available = false; opening = command(""); },
    close: async () => { await press("\x1b"); await opening; },
    prompt: () => handlers.get("before_agent_start")({ systemPrompt: "base" }).systemPrompt,
  };
}

test("bare security opens a native panel without writing; RPC with hasUI gets status", async () => {
  const ui = await fixture();
  const before = readFileSync(ui.configPath, "utf8");
  expect(ui.screen()).toContain("Credentials filtered");
  expect(ui.screen()).toContain("auto mode reviews each requested command");
  await ui.close();
  expect(ui.closed()).toBe(true);
  expect(readFileSync(ui.configPath, "utf8")).toBe(before);
  const rpc = await fixture("rpc");
  expect(rpc.notices.at(-1)).toContain("credential filtering: on");
});

test("toggles persist and immediately change protected tools and agent guidance", async () => {
  const ui = await fixture();
  const cacheFile = join(ui.root, "cache", "artifact");
  await ui.run("write", { path: cacheFile, content: "compiled" });
  await ui.choose("Build-cache access");
  expect(ui.config().buildCaches.enabled).toBe(false);
  await expect(ui.run("write", { path: cacheFile, content: "blocked" })).rejects.toThrow("outside allowed");
  expect(ui.prompt()).toContain("Build-cache grants: disabled");
  await ui.choose("Build-cache access");
  await ui.run("write", { path: cacheFile, content: "works again" });
  await ui.choose("Tool network");
  expect(ui.config().network).toBe("deny");
  expect(ui.prompt()).toContain("Tool network: deny");
  await ui.choose("OS sandbox");
  const outside = join(ui.root, "outside", "file");
  await ui.run("write", { path: outside, content: "sandbox off" });
  expect(ui.screen()).toContain("Credentials filtered");
  expect(ui.screen()).toContain("(inactive)");
  await ui.choose("OS sandbox");
  await expect(ui.run("write", { path: outside, content: "blocked" })).rejects.toThrow("outside allowed");
  await ui.close();
  ui.reopen();
  await ui.choose("Extra access review");
  expect(ui.screen()).toContain("configured reviewer");
});

test("cache paths support add, validated atomic edit, cancel and removal without deleting files", async () => {
  const ui = await fixture();
  await ui.choose("Cache directories");
  await ui.choose("+ Add path");
  await ui.press("../cache with spaces");
  await ui.press("\r");
  const cache = join(ui.root, "cache with spaces");
  expect(existsSync(cache)).toBe(true);
  await ui.run("write", { path: join(cache, "artifact"), content: "compiled" });
  await ui.choose(cache);
  await ui.choose("Edit path");
  await ui.press("\x15");
  await ui.press("/");
  await ui.press("\r");
  expect(ui.screen()).toContain("specific build cache");
  expect(ui.config().buildCaches.paths).toContain(cache);
  await ui.press("\x1b");
  await ui.choose("Edit path");
  await ui.press("\x15");
  await ui.press("../replacement-cache");
  await ui.press("\r");
  expect(ui.config().buildCaches.paths).not.toContain(cache);
  expect(existsSync(join(cache, "artifact"))).toBe(true);
  const replacement = join(ui.root, "replacement-cache");
  await ui.choose(replacement);
  await ui.choose("Remove from this list");
  expect(ui.config().buildCaches.paths).not.toContain(replacement);
  expect(existsSync(replacement)).toBe(true);
});

test("extra write grants validate directories and restore containment on removal", async () => {
  const ui = await fixture();
  await ui.choose("Extra writable directories");
  await ui.choose("+ Add path");
  await ui.press("../missing");
  await ui.press("\r");
  expect(ui.screen()).toContain("existing directory");
  expect(ui.config().allowWrite).toEqual([]);
  await ui.press("\x15");
  await ui.press("../outside");
  await ui.press("\r");
  const outside = join(ui.root, "outside");
  await ui.run("write", { path: join(outside, "file"), content: "ok" });
  await ui.choose(outside);
  await ui.choose("Remove from this list");
  await expect(ui.run("write", { path: join(outside, "file"), content: "blocked" })).rejects.toThrow("outside allowed");
});

test("read and write protections are editable; built-in protections cannot be removed", async () => {
  const ui = await fixture();
  const target = join(ui.root, "project", "private");
  mkdirSync(target);
  const file = join(target, "file");
  writeFileSync(file, "private contents");
  for (const [label, field, tool, args] of [
    ["Protected reads", "denyRead", "read", { path: file }],
    ["Protected writes", "denyWrite", "write", { path: file, content: "changed" }],
  ] as const) {
    await ui.choose(label);
    await ui.choose("+ Add path");
    await ui.press(target);
    await ui.press("\r");
    expect(ui.config()[field]).toContain(target);
    await expect(ui.run(tool, args)).rejects.toThrow("explicitly protected");
    await ui.choose(target);
    await ui.choose("Remove from this list");
    await ui.run(tool, args);
    await ui.press("\x1b");
  }
  await ui.choose("Protected writes");
  await ui.choose(ui.configPath);
  expect(ui.screen().replace(/\s+/g, " ")).toContain("cannot be removed");
  expect(ui.screen()).not.toContain("Edit path");
  expect(ui.screen()).not.toContain("Remove from this list");
});

test("restore preset has a cancel path and preserves files", async () => {
  const ui = await fixture();
  await ui.choose("OS sandbox");
  await ui.choose("Cache directories");
  await ui.choose("Restore standard cache preset");
  await ui.press("\r");
  expect(ui.config().buildCaches.paths).toEqual([join(ui.root, "cache")]);
  await ui.choose("Restore standard cache preset");
  await ui.choose("Restore and enable");
  expect(ui.config().buildCaches).toEqual(defaultBuildCaches());
  expect(existsSync(join(ui.root, "cache"))).toBe(true);
});

test("resizing preserves selection and keeps navigation visible in a narrow terminal", async () => {
  const ui = await fixture();
  await ui.select("Protected reads");
  ui.resize(20);
  const lines = ui.screen(36).split("\n");
  expect(lines.length).toBeLessThanOrEqual(18);
  expect(lines.every((line) => visibleWidth(line) <= 36)).toBe(true);
  expect(lines.join("\n")).toContain("→ Protected reads");
  expect(lines.join("\n")).toContain("Esc close");
  await ui.press("\r");
  await ui.choose("+ Add path");
  await ui.press("draft path");
  const before = readFileSync(ui.configPath, "utf8");
  ui.resize(45);
  expect(ui.screen()).toContain("draft path");
  await ui.press("\x1b");
  expect(readFileSync(ui.configPath, "utf8")).toBe(before);
});

test("save failures stay visible without claiming a toggle succeeded", async () => {
  const ui = await fixture();
  const panel = new SecurityPanel({ config: ui.config(), configPath: ui.configPath, cwd: ui.root, reviewAvailable: true }, theme,
    () => 40, () => {}, async () => { throw new Error("Settings disk is read-only"); }, () => {});
  await panel.handleInput("\r");
  expect(panel.render(100).join("\n")).toContain("Settings disk is read-only");
  expect(panel.render(100).join("\n")).toContain("OS sandbox · on");
  expect(ui.config().enabled).toBe(true);
});
