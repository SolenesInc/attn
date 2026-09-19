import { afterEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { defaultApprovalConfig, type ApprovalConfig } from "../approval/config";
import { retryWithoutSandboxReason } from "../approval/orchestrator";
import { presetByID, type Preset } from "../approval/presets";
import { commandOptions } from "../approval/reviewers";
import { PiApproval, statusKey, type Permissions } from "../approval/session";
import { sandboxSpecFor, type SandboxSpec } from "../sandbox/index";
import { canonical, loadSecurityConfig, resolveSecurityPolicy } from "../security/policy";

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

const preset = (id: string): Preset => presetByID(id)!;
const pair = (id: string): Permissions => ({ approvalPolicy: preset(id).approvalPolicy, sandboxMode: preset(id).sandboxMode });

/** A registered session with the real security policy for its paths, answering its own
 * approval cards, and recording everything the session paints, asks or says. */
async function fixture(config: Partial<ApprovalConfig> = {}, options: { mode?: string; confirm?: boolean } = {}) {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-permissions-")));
  roots.push(root);
  for (const name of ["project", "temp"]) mkdirSync(join(root, name));
  const configPath = join(root, "attn-security.json");
  writeFileSync(configPath, JSON.stringify({ enabled: true, network: "allow" }));
  const policy = resolveSecurityPolicy(loadSecurityConfig(configPath), join(root, "project"), configPath, join(root, "temp"));
  const approval = new PiApproval({
    // Network off keeps the missing-proxy warning out of the notices these tests read.
    config: { ...defaultApprovalConfig, network: { ...defaultApprovalConfig.network, enabled: false }, ...config },
    suite: {
      networkDecider: undefined,
      reportDenial: () => {},
      reportApprovalWindow: () => {},
      reportExecPolicyAmendment: async () => {},
      reportNetworkAmendment: async () => {},
    },
    ledger: { record: () => {} },
  });
  approval.useSandbox(policy);
  const commands = new Map<string, { description: string; handler: (args: string, ctx: unknown) => unknown }>();
  const handlers = new Map<string, (event: unknown, ctx: unknown) => unknown>();
  approval.register({
    registerFlag: () => {},
    registerCommand: (name: string, command: unknown) => commands.set(name, command as never),
    getFlag: () => undefined,
    appendEntry: () => {},
    on: (name: string, handler: (event: unknown, ctx: unknown) => unknown) => handlers.set(name, handler),
  } as never);
  const notices: { text: string; level: string }[] = [];
  const statuses: string[] = [];
  const titles: string[] = [];
  const confirms: string[] = [];
  let output = "";
  const ctx = {
    cwd: policy.cwd,
    mode: options.mode ?? "tui",
    hasUI: true,
    toolCallId: "call-1",
    onData: (data: Buffer) => { output += data.toString(); },
    ui: {
      notify: (text: string, level: string) => { notices.push({ text, level }); },
      setStatus: (key: string, text: string) => { if (key === statusKey) statuses.push(text); },
      confirm: async (title: string) => { confirms.push(title); return options.confirm ?? false; },
      select: async (title: string) => { titles.push(title); return commandOptions.approve; },
    },
  };
  await handlers.get("session_start")!({}, ctx);
  return {
    approval, root, notices, statuses, titles, confirms,
    output: () => output,
    spec: (): SandboxSpec | "unsandboxed" => {
      const source = approval.sandboxSource();
      return sandboxSpecFor(source.config, source.cwd, source.temp, { permissions: "use_default" });
    },
    permissions: (text: string) => commands.get("permissions")!.handler(text, ctx),
    run: (command: string) => approval.runBash({ command } as never, ctx as never),
    startAnotherSession: () => handlers.get("session_start")!({}, ctx),
    auto: (text: string) => commands.get("auto")!.handler(text, ctx),
  };
}

function wrapper(spec: SandboxSpec | "unsandboxed"): SandboxSpec {
  if (spec === "unsandboxed") throw new Error("the command would run with no sandbox wrapper at all");
  return spec;
}

test("a switch to read-only changes the sandbox the next command is wrapped in", async () => {
  const it = await fixture(pair("default"));

  expect(wrapper(it.spec()).mode).toBe("workspace-write");
  expect(wrapper(it.spec()).writableRoots).toContain(join(it.root, "project"));

  await it.approval.setPermissions(pair("read-only"));

  expect(wrapper(it.spec()).mode).toBe("read-only");
  expect(wrapper(it.spec()).writableRoots).toEqual([]);
});

test("a switch to full-access drops the sandbox wrapper entirely", async () => {
  const it = await fixture(pair("default"));
  expect(wrapper(it.spec()).mode).toBe("workspace-write");

  await it.approval.setPermissions(pair("full-access"));

  expect(it.spec()).toBe("unsandboxed");
});

test.skipIf(process.platform !== "darwin")(
  "the unsandboxed retry engages only once the session is untrusted",
  async () => {
    const outside = canonical(mkdtempSync(join(tmpdir(), "pi-permissions-outside-")));
    roots.push(outside);
    const it = await fixture(pair("default"));
    const target = join(outside, "artifact");
    const write = `printf built > ${JSON.stringify(target)}`;

    const denied = await it.run(write);
    expect(denied.exitCode).not.toBe(0);
    expect(existsSync(target)).toBe(false);
    expect(it.titles).toEqual([]);

    await it.approval.setPermissions(pair("untrusted"));

    const allowed = await it.run(write);
    expect(allowed.exitCode).toBe(0);
    expect(readFileSync(target, "utf8")).toBe("built");
    expect(it.titles).toHaveLength(2);
    expect(it.titles[1]).toContain(retryWithoutSandboxReason);
  },
);

test("/permissions status names the preset, the pair and what a relaunch does", async () => {
  const it = await fixture(pair("default"));

  await it.permissions("status");

  expect(it.notices).toEqual([{
    text: "Permissions: default (on-request, workspace-write). Set at launch by attn; " +
      "/permissions changes this session only, and a relaunch returns to the launch choice.",
    level: "info",
  }]);
});

test("/permissions status says so when the pair matches no preset", async () => {
  const it = await fixture({ approvalPolicy: "never", sandboxMode: "read-only" });

  await it.permissions("status");

  expect(it.notices.at(-1)!.text).toStartWith("Permissions: never/read-only (no preset).");
});

test("/permissions refuses an id that is not a preset and changes nothing", async () => {
  const it = await fixture(pair("default"));

  await it.permissions("yolo");

  expect(it.notices).toEqual([{
    text: 'unknown permissions preset "yolo"; use read-only, default, full-access, untrusted or status',
    level: "error",
  }]);
  expect(it.approval.permissions()).toEqual(pair("default"));
});

test("full-access asks first, and a declined confirmation leaves the session where it was", async () => {
  const it = await fixture(pair("default"), { confirm: false });

  await it.permissions("full-access");

  expect(it.confirms).toEqual(["Enable full access?"]);
  expect(it.approval.permissions()).toEqual(pair("default"));
  expect(wrapper(it.spec()).mode).toBe("workspace-write");
  expect(it.notices.at(-1)!.text).toBe("Permissions unchanged: default (on-request, workspace-write).");
});

test("an accepted full-access confirmation applies the preset", async () => {
  const it = await fixture(pair("default"), { confirm: true });

  await it.permissions("full-access");

  expect(it.confirms).toEqual(["Enable full access?"]);
  expect(it.approval.permissions()).toEqual(pair("full-access"));
  expect(it.spec()).toBe("unsandboxed");
  expect(it.notices.at(-1)!.text).toBe("Permissions: full-access (never, danger-full-access) for this session");
});

test("a preset that is not full-access applies with no confirmation and repaints the status line", async () => {
  const it = await fixture(pair("default"));
  expect(it.statuses.at(-1)).toBe("auto: off · default");

  await it.permissions("read-only");

  expect(it.confirms).toEqual([]);
  expect(it.statuses.at(-1)).toBe("auto: off · read-only");
});

test("the status line falls back to the raw pair when no preset matches", async () => {
  const it = await fixture({ approvalPolicy: "untrusted", sandboxMode: "read-only" });

  expect(it.statuses.at(-1)).toBe("auto: off · untrusted/read-only");
});

test("a new session in the same process starts from the launch pair again", async () => {
  const it = await fixture(pair("default"));
  await it.approval.setPermissions(pair("full-access"));
  expect(it.spec()).toBe("unsandboxed");

  await it.startAnotherSession();

  expect(it.approval.permissions()).toEqual(pair("default"));
  expect(wrapper(it.spec()).mode).toBe("workspace-write");
  expect(it.statuses.at(-1)).toBe("auto: off · default");
});

test("a new session in the same process starts from the launch auto mode again", async () => {
  const it = await fixture({ ...pair("default"), enabledDefault: false });
  expect(it.approval.enabled()).toBe(false);

  it.auto("on");
  expect(it.approval.enabled()).toBe(true);

  await it.startAnotherSession();

  // The footer is not the assertion here: this fixture has no model registry, so
  // it has no reviewer and paints `auto: off` whatever the answer was.
  expect(it.approval.enabled()).toBe(false);
  expect(it.statuses.at(-1)).toBe("auto: off · default");
});
