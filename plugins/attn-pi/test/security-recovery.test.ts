import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { CredentialFilter } from "../security/filter";
import { SandboxedFilesystem } from "../security/filesystem";
import { canonical, loadSecurityConfig, resolveSecurityPolicy } from "../security/policy";
import { protectedTools } from "../security/tools";
import { scopedPolicy, type SandboxReview } from "../security/recovery";
import { shellQuote } from "../security/sandbox";
import { PiSecurity } from "../security/index";
import { createAutoMode, type AutoModeDenial, type ToolCallReview } from "../automode/index";
import { defaultAutoModeConfig } from "../automode/config";
import { StubClassifier } from "../automode/classifier";
import { AutoMode } from "../automode/mode";
import { FakePi, ctx, toolCall } from "./automode-fake-pi";

const cleanup: (() => Promise<void>)[] = [];
afterEach(async () => { for (const close of cleanup.splice(0)) await close(); });

function fixture(review?: SandboxReview) {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-recovery-test-")));
  for (const name of ["project", "cache", "other-cache", "private", "agent", "temp"]) mkdirSync(join(root, name));
  const configPath = join(root, "agent", "attn-security.json");
  const config = { ...loadSecurityConfig(configPath), denyRead: [join(root, "private")], denyWrite: [join(root, "private"), join(root, "agent")] };
  config.buildCaches.enabled = false;
  writeFileSync(configPath, JSON.stringify(config));
  const policy = resolveSecurityPolicy(config, join(root, "project"), configPath, join(root, "temp"));
  const filter = new CredentialFilter({ PROVIDER_API_KEY: "synthetic-recovery-credential" });
  const fs = new SandboxedFilesystem(policy, filter);
  cleanup.push(async () => { await fs.close(); rmSync(root, { recursive: true, force: true }); });
  const bash = protectedTools(policy, filter, fs, review).find((tool) => tool.name === "bash")!;
  const run = (args: Record<string, unknown>, signal?: AbortSignal) => bash.execute("recovery-call", args, signal, undefined, undefined as never);
  return { root, policy, filter, fs, run };
}

test("a failed cache write explains the restriction and the reviewed retry; unrelated errors stay unchanged", async () => {
  const { root, run } = fixture(async () => undefined);
  const command = `printf built > ${shellQuote(join(root, "cache", "artifact"))}`;
  await expect(run({ command })).rejects.toThrow("retry bash with sandbox:");
  await expect(run({ command: "printf 'ordinary compilation error' >&2; exit 2" })).rejects.toThrow("ordinary compilation error\n\nCommand exited with code 2");
});

test("a pre-approved command still reviews its extra scope and succeeds only for that execution", async () => {
  const classifier = new StubClassifier({ verdict: "allow" });
  let review!: ToolCallReview;
  const pi = new FakePi();
  createAutoMode({ config: { ...defaultAutoModeConfig, allow: ["printf *"] }, classifier,
    sandboxReviewInExecutor: true, onReady: (ready) => { review = ready; } })(pi);
  const { root, policy, run } = fixture((event, context) => review(event, context));
  const command = `printf synthetic-recovery-credential; printf built > ${shellQuote(join(root, "cache", "artifact"))}`;
  const sandbox = { allowWrite: [join(root, "cache")], reason: "The build stores its artifacts in this cache." };
  expect(await pi.toolCall!(toolCall("bash", { command, sandbox }), ctx)).toBeUndefined();
  expect(classifier.requests).toHaveLength(0);
  const result = await run({ command, sandbox });
  expect(classifier.requests).toHaveLength(1);
  expect(classifier.requests[0]!.call.input.sandbox).toEqual(sandbox);
  expect(readFileSync(join(root, "cache", "artifact"), "utf8")).toBe("built");
  expect(JSON.stringify(result)).toContain("approved temporary sandbox access");
  expect(JSON.stringify(result)).not.toContain("synthetic-recovery-credential");
  expect(JSON.parse(readFileSync(policy.configPath, "utf8")).allowWrite).toEqual([]);
  await expect(run({ command })).rejects.toThrow("permission error");
});

test("auto mode refusal records scope and tells the agent how user approval is reconsidered", async () => {
  const classifier = new StubClassifier({ verdict: "deny", reason: "The user has not approved changes to shared build artifacts." });
  const denials: AutoModeDenial[] = [];
  let review!: ToolCallReview;
  const pi = new FakePi();
  createAutoMode({ config: defaultAutoModeConfig, classifier, onDenial: (denial) => denials.push(denial), onReady: (ready) => { review = ready; } })(pi);
  pi.say("Build the project.");
  const { root, run } = fixture((event, context) => review(event, context));
  const command = `printf built > ${shellQuote(join(root, "cache", "artifact"))}`;
  const sandbox = { allowWrite: [join(root, "cache")], reason: "Build output cache" };
  await expect(run({ command, sandbox })).rejects.toThrow("retry the same bash request");
  expect(existsSync(join(root, "cache", "artifact"))).toBe(false);
  expect(denials[0]!.action).toContain(join(root, "cache"));
  expect(denials[0]!.action).toContain("Build output cache");
  pi.say(`I approve ${command} with write access to ${join(root, "cache")}.`);
  classifier.answerWith({ verdict: "allow" });
  await run({ command, sandbox });
  expect(classifier.requests.at(-1)!.transcript!.some((entry) => entry.text.includes("I approve"))).toBe(true);
  expect(existsSync(join(root, "cache", "artifact"))).toBe(true);
});

test("hard-denied commands keep their denial even with a sandbox request", async () => {
  let review!: ToolCallReview;
  const classifier = new StubClassifier({ verdict: "allow" });
  createAutoMode({ config: { ...defaultAutoModeConfig, hardDeny: ["printf *"] }, classifier, onReady: (ready) => { review = ready; } })(new FakePi());
  const { root, run } = fixture((event, context) => review(event, context));
  await expect(run({ command: "printf no", sandbox: { allowWrite: [join(root, "cache")], reason: "cache" } })).rejects.toThrow("User confirmation cannot override");
  expect(classifier.requests).toHaveLength(0);
});

test("missing reviewer and disabled auto mode refuse scope without running the command", async () => {
  const mode = new AutoMode({ config: { ...defaultAutoModeConfig, models: ["test/judge"], enabledDefault: false }, sandboxReviewInExecutor: true });
  mode.register(new FakePi());
  for (const review of [undefined, mode.reviewSandbox]) {
    const { root, run } = fixture(review);
    await expect(run({ command: `touch ${shellQuote(join(root, "project", "ran"))}`, sandbox: { allowWrite: [join(root, "cache")], reason: "cache" } })).rejects.toThrow("auto mode is off or no reviewer is configured");
    expect(existsSync(join(root, "project", "ran"))).toBe(false);
  }
});

test("explicitly protected paths are refused before review and ancestor grants retain those protections", async () => {
  let reviews = 0;
  const { root, run } = fixture(async () => { reviews++; });
  await expect(run({ command: "true", sandbox: { allowWrite: [join(root, "private")], reason: "need secrets" } })).rejects.toThrow("explicitly protected");
  expect(reviews).toBe(0);
  await expect(run({ command: `printf no > ${shellQuote(join(root, "private", "secret"))}`, sandbox: { allowWrite: [root], reason: "test ancestor scope" } })).rejects.toThrow();
  expect(existsSync(join(root, "private", "secret"))).toBe(false);
});

test("concurrent calls cannot borrow each other's scoped grants", async () => {
  const { root, run } = fixture(async () => undefined);
  const result = await Promise.allSettled([
    run({ command: `printf own > ${shellQuote(join(root, "cache", "own"))}`, sandbox: { allowWrite: [join(root, "cache")], reason: "own cache" } }),
    run({ command: `printf borrowed > ${shellQuote(join(root, "cache", "borrowed"))}`, sandbox: { allowWrite: [join(root, "other-cache")], reason: "other cache" } }),
  ]);
  expect(result.map((item) => item.status)).toEqual(["fulfilled", "rejected"]);
  expect(existsSync(join(root, "cache", "borrowed"))).toBe(false);
});

test("cancellation while review is pending prevents execution", async () => {
  const controller = new AbortController();
  const { root, run } = fixture(async () => { controller.abort(); });
  await expect(run({ command: `touch ${shellQuote(join(root, "cache", "ran"))}`, sandbox: { allowWrite: [join(root, "cache")], reason: "cache" } }, controller.signal)).rejects.toThrow("cancelled");
  expect(existsSync(join(root, "cache", "ran"))).toBe(false);
});

test("reviewed network access is temporary and does not grant unrelated writes", async () => {
  const { root, policy, filter, fs } = fixture();
  policy.network = "deny";
  const bash = protectedTools(policy, filter, fs, async () => undefined).find((tool) => tool.name === "bash")!;
  let connections = 0;
  const server = createServer((socket) => { connections++; socket.end(); });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const port = (server.address() as { port: number }).port;
    const script = `const s=require('net').connect(${port},'127.0.0.1');s.on('connect',()=>{console.log('connected');s.destroy()});s.on('error',()=>process.exit(1));`;
    const command = `${shellQuote(process.execPath)} -e ${shellQuote(script)}`;
    const execute = (args: Record<string, unknown>) => bash.execute("network", args, undefined, undefined, undefined as never);
    await expect(execute({ command })).rejects.toThrow();
    const result = await execute({ command, sandbox: { network: "allow", reason: "Fetch build dependency" } });
    expect(JSON.stringify(result)).toContain("connected");
    await expect(execute({ command })).rejects.toThrow();
    await expect(execute({ command: `touch ${shellQuote(join(root, "cache", "not-granted"))}`, sandbox: { network: "allow", reason: "Network only" } })).rejects.toThrow();
    expect(connections).toBe(1);
    expect(existsSync(join(root, "cache", "not-granted"))).toBe(false);
  } finally { await new Promise<void>((resolve) => server.close(() => resolve())); }
});

test("an unpaired auto-mode extension still reviews sandbox-shaped calls", async () => {
  const classifier = new StubClassifier({ verdict: "deny", reason: "Not authorized" });
  const pi = new FakePi();
  createAutoMode({ config: { ...defaultAutoModeConfig, allow: ["git status"] }, classifier })(pi);
  const result = await pi.toolCall!(toolCall("bash", { command: "git status", sandbox: { allowWrite: ["/cache"], reason: "cache" } }), ctx);
  expect(result?.block).toBe(true);
  expect(classifier.requests).toHaveLength(1);
});

test("a directory replaced with a symlink during review needs a new review", async () => {
  const { root, run } = fixture(async () => {
    rmSync(join(root, "cache"), { recursive: true });
    symlinkSync(join(root, "other-cache"), join(root, "cache"));
  });
  await expect(run({ command: `touch ${shellQuote(join(root, "cache", "ran"))}`, sandbox: { allowWrite: [join(root, "cache")], reason: "cache" } })).rejects.toThrow("changed during review");
  expect(existsSync(join(root, "other-cache", "ran"))).toBe(false);
});

test("scope validation rejects absent directories and unsupported permission changes", () => {
  const { root, policy } = fixture();
  expect(() => scopedPolicy(policy, { reason: "nothing" })).toThrow("needs allowWrite");
  expect(() => scopedPolicy(policy, { allowWrite: [join(root, "missing")], reason: "cache" })).toThrow("existing directory");
  expect(() => scopedPolicy(policy, { allowWrite: ["/"], reason: "everything" })).toThrow("specific cache");
  expect(() => scopedPolicy(policy, { allowWrite: [root], reason: "cache", enabled: false } as never)).toThrow("cannot disable");
});

test("a security reconfiguration during review invalidates the pending execution", async () => {
  const { root, policy } = fixture();
  const handlers = new Map<string, any>();
  const tools = new Map<string, any>();
  const commands = new Map<string, any>();
  const context = { cwd: policy.cwd, ui: { setStatus() {}, notify() {} } };
  const security = new PiSecurity(policy.configPath, async () => { await commands.get("security").handler("network deny", context); });
  security.register({ on: (name: string, fn: any) => handlers.set(name, fn), registerTool: (tool: any) => tools.set(tool.name, tool), registerCommand: (name: string, command: any) => commands.set(name, command) } as never);
  try {
    await handlers.get("session_start")({}, context);
    await expect(tools.get("bash").execute("stale", { command: `touch ${shellQuote(join(root, "cache", "ran"))}`, sandbox: { allowWrite: [join(root, "cache")], reason: "cache" } }, undefined, undefined, context)).rejects.toThrow("changed during review");
    expect(existsSync(join(root, "cache", "ran"))).toBe(false);
  } finally { await handlers.get("session_shutdown")({}, context); }
});
