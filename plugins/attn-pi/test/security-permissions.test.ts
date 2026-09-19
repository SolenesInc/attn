import { afterEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { defaultApprovalConfig, type ApprovalConfig } from "../approval/config";
import { presetByID } from "../approval/presets";
import { PiApproval, type Permissions } from "../approval/session";
import { CredentialFilter } from "../security/filter";
import { SandboxedFilesystem } from "../security/filesystem";
import { PiSecurity } from "../security/index";
import { loadSecurityConfig, resolveSecurityPolicy, type SecurityPolicy } from "../security/policy";
import { protectedTools } from "../security/tools";
import type { SandboxMode } from "../sandbox/spec";
import { fixtureRoot } from "./fixture-root";

const directories: string[] = [];
const workers: SandboxedFilesystem[] = [];
afterEach(async () => {
  await Promise.all(workers.splice(0).map((worker) => worker.close()));
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

const pair = (id: string): Permissions => {
  const preset = presetByID(id)!;
  return { approvalPolicy: preset.approvalPolicy, sandboxMode: preset.sandboxMode };
};

/** A real policy on disk: project is writable, private is denied, outside is neither. */
function paths(prefix: string): { root: string; configPath: string; policy: SecurityPolicy } {
  const root = fixtureRoot(prefix);
  directories.push(root);
  for (const folder of ["project", "private", "scratch", "agent", "outside"]) mkdirSync(join(root, folder));
  const configPath = join(root, "agent", "attn-security.json");
  const config = loadSecurityConfig(configPath);
  config.buildCaches.enabled = false;
  config.denyWrite = [join(root, "private")];
  writeFileSync(configPath, JSON.stringify(config));
  return { root, configPath, policy: resolveSecurityPolicy(config, join(root, "project"), configPath, join(root, "scratch")) };
}

/** The native tools over a real worker running under one sandbox mode. */
function tools(policy: SecurityPolicy, sandboxMode: SandboxMode) {
  const fs = new SandboxedFilesystem(policy, new CredentialFilter({}), sandboxMode);
  workers.push(fs);
  const built = protectedTools(policy, new CredentialFilter({}), fs);
  return (name: string, input: unknown) =>
    built.find((tool) => tool.name === name)!.execute("test", input, undefined, undefined, undefined as never);
}

/** PiSecurity and PiApproval built and wired the way suite/index.ts does, so a
 * permissions switch reaches the native file tools through the real listener. */
async function wired(config: Partial<ApprovalConfig> = {}) {
  const { root, configPath, policy } = paths("pi-permissions-live-");
  const approval = new PiApproval({
    config: { ...defaultApprovalConfig, network: { ...defaultApprovalConfig.network, enabled: false }, ...config },
    suite: {
      networkDecider: undefined, reportDenial: () => {}, reportApprovalWindow: () => {},
      reportExecPolicyAmendment: async () => {}, reportNetworkAmendment: async () => {},
    },
    ledger: { record: () => {} },
  });
  const security = new PiSecurity(configPath, approval.runBash, (paths) => approval.useSandbox(paths), undefined,
    () => approval.permissions().sandboxMode);
  approval.onPermissions(() => security.refresh());

  const registered = new Map<string, { execute: (...args: never[]) => unknown }>();
  const handlers = new Map<string, ((event: unknown, ctx: unknown) => unknown)[]>();
  const api = {
    on: (name: string, handler: (event: unknown, ctx: unknown) => unknown) => {
      handlers.set(name, [...handlers.get(name) ?? [], handler]);
    },
    registerTool: (tool: { name: string; execute: (...args: never[]) => unknown }) => registered.set(tool.name, tool),
    registerCommand: () => {}, registerFlag: () => {}, getFlag: () => undefined, appendEntry: () => {},
  };
  security.register(api as never);
  approval.register(api as never);
  const ctx = {
    cwd: policy.cwd, mode: "rpc", hasUI: false,
    ui: { notify: () => {}, setStatus: () => {}, confirm: async () => false },
  };
  const start = async () => {
    for (const handler of handlers.get("session_start") ?? []) await handler({}, ctx);
  };
  await start();
  return {
    root, approval, start,
    run: (name: string, input: unknown) => call(registered.get(name)!)(input),
    /** A tool closure held from before a switch, the way a turn already in flight holds one. */
    capture: (name: string) => call(registered.get(name)!),
    switchTo: (id: string) => approval.setPermissions(pair(id)),
    close: async () => { for (const handler of handlers.get("session_shutdown") ?? []) await handler({}, ctx); },
  };
}

const call = (tool: { execute: (...args: never[]) => unknown }) =>
  async (input: unknown): Promise<unknown> =>
    await tool.execute("test", input as never, undefined as never, undefined as never, undefined as never);

const refusal = (error: unknown) => (error instanceof Error ? error.message : String(error));

test("read-only refuses the write and edit tools inside the workspace and still reads", async () => {
  const { policy } = paths("pi-permissions-readonly-");
  const target = join(policy.cwd, "sample.txt");
  writeFileSync(target, "hello world\n");
  const run = tools(policy, "read-only");

  const written = await run("write", { path: "sample.txt", content: "replaced\n" }).catch(refusal);
  const edited = await run("edit", { path: "sample.txt", edits: [{ oldText: "world", newText: "agent" }] }).catch(refusal);

  for (const message of [JSON.stringify(written), JSON.stringify(edited)]) {
    expect(message).toContain("This session's permissions are read-only");
    expect(message).toContain("Ask the user to change /permissions");
    expect(message).toContain("sandbox_permissions=require_escalated");
  }
  expect(readFileSync(target, "utf8")).toBe("hello world\n");
  expect(JSON.stringify(await run("read", { path: "sample.txt" }))).toContain("hello world");
});

test("danger-full-access writes outside the allowed roots but still honours a deny", async () => {
  const { root, policy } = paths("pi-permissions-danger-");
  const outside = join(root, "outside", "artifact.txt");
  const denied = join(root, "private", "artifact.txt");

  const confined = await tools(policy, "workspace-write")("write", { path: outside, content: "nope\n" }).catch(refusal);
  expect(JSON.stringify(confined)).toContain("outside allowed paths");
  expect(existsSync(outside)).toBe(false);

  const run = tools(policy, "danger-full-access");
  await run("write", { path: outside, content: "built\n" });
  expect(readFileSync(outside, "utf8")).toBe("built\n");
  await run("edit", { path: outside, edits: [{ oldText: "built", newText: "rebuilt" }] });
  expect(readFileSync(outside, "utf8")).toBe("rebuilt\n");

  const blocked = await run("write", { path: denied, content: "nope\n" }).catch(refusal);
  expect(JSON.stringify(blocked)).toContain("explicitly protected");
  expect(existsSync(denied)).toBe(false);
});

test("a permissions switch reaches the native file tools while the session runs", async () => {
  const session = await wired(pair("default"));
  await session.run("write", { path: "first.txt", content: "one\n" });

  await session.switchTo("read-only");
  const refused = await session.run("write", { path: "second.txt", content: "two\n" }).catch(refusal);
  expect(JSON.stringify(refused)).toContain("This session's permissions are read-only");
  expect(existsSync(join(session.root, "project", "second.txt"))).toBe(false);

  await session.switchTo("default");
  await session.run("write", { path: "second.txt", content: "two\n" });
  expect(readFileSync(join(session.root, "project", "second.txt"), "utf8")).toBe("two\n");
  await session.close();
});

test("a new session restores the launch permissions for the native file tools too", async () => {
  const session = await wired(pair("default"));
  const outside = join(session.root, "outside", "artifact.txt");

  await session.switchTo("full-access");
  await session.run("write", { path: outside, content: "built\n" });
  expect(readFileSync(outside, "utf8")).toBe("built\n");

  rmSync(outside);
  await session.start();

  const refused = await session.run("write", { path: outside, content: "again\n" }).catch(refusal);
  expect(JSON.stringify(refused)).toContain("outside allowed paths");
  expect(existsSync(outside)).toBe(false);
  await session.close();
});

test("a switch reaches the tools before it returns, with the old worker still in flight", async () => {
  const session = await wired(pair("full-access"));
  const artifact = join(session.root, "outside", "artifact.txt");
  await session.run("write", { path: artifact, content: "built\n" });
  expect(readFileSync(artifact, "utf8")).toBe("built\n");

  await session.approval.setPermissions(pair("read-only"));

  const second = join(session.root, "outside", "second.txt");
  const refused = await session.run("write", { path: second, content: "again\n" }).catch(refusal);
  expect(JSON.stringify(refused)).toContain("This session's permissions are read-only");
  expect(existsSync(second)).toBe(false);
  await session.close();
});

test("a tool held from before the switch cannot bring its old worker back", async () => {
  const session = await wired(pair("full-access"));
  const artifact = join(session.root, "outside", "artifact.txt");
  const write = session.capture("write");
  await write({ path: artifact, content: "built\n" });
  rmSync(artifact);

  await session.switchTo("read-only");

  const refused = await write({ path: artifact, content: "again\n" }).catch(refusal);
  expect(JSON.stringify(refused)).toContain("were rebuilt after a permissions change");
  expect(existsSync(artifact)).toBe(false);
  await session.close();
});
