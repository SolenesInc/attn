import { afterEach, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, existsSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createLocalBashOperations } from "@earendil-works/pi-coding-agent";
import { ApprovalOrchestrator, retryWithoutSandboxReason, turnAbortedMessage } from "../approval/orchestrator";
import { commandOptions, userRejection } from "../approval/reviewers";
import { PiApproval } from "../approval/session";
import { defaultApprovalConfig, type ApprovalConfig } from "../approval/config";
import type { ApprovalRequest, ReviewDecision, Reviewer } from "../approval/types";
import type { ApprovalPolicy, PrefixRule, SandboxMode } from "../execpolicy/index";
import type { NetworkRequest } from "../netproxy/index";
import { sandboxSpecFor } from "../sandbox/index";
import { canonical, loadSecurityConfig, resolveSecurityPolicy } from "../security/policy";

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

type Script = (request: ApprovalRequest) => ReviewDecision;

function fixture(options: {
  script: Script;
  rules?: PrefixRule[];
  approvalPolicy?: ApprovalPolicy;
  sandboxMode?: SandboxMode;
} ) {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-approval-test-")));
  roots.push(root);
  const seen: ApprovalRequest[] = [];
  const denials: { rule: string; reason: string; action: string }[] = [];
  const amendments: { pattern: string[] }[] = [];
  const hosts: string[] = [];
  const reviewer: Reviewer = {
    name: "user",
    review: async (request) => { seen.push(structuredClone(request)); return options.script(request); },
  };
  const local = createLocalBashOperations({ shellPath: "/bin/bash" });
  let announce: () => void = () => {};
  const firstRun = new Promise<void>((resolve) => { announce = resolve; });
  const orchestrator = new ApprovalOrchestrator({
    approvalPolicy: () => options.approvalPolicy ?? "untrusted",
    sandboxMode: () => options.sandboxMode ?? "danger-full-access",
    sandbox: () => ({
      config: {
        mode: options.sandboxMode ?? "danger-full-access",
        network: false,
        allowWrite: [root],
        denyRead: [],
        denyWrite: [],
        cacheWritePaths: [],
      },
      cwd: root,
      temp: root,
    }),
    reviewer: () => reviewer,
    rules: options.rules ?? [],
    run: (command, cwd, run) => { announce(); return local.exec(command, cwd, run); },
    onDenial: (denial) => denials.push({ rule: denial.rule, reason: denial.reason, action: denial.action }),
    onExecPolicyAmendment: (pattern) => amendments.push({ pattern }),
    onNetworkAmendment: (host) => hosts.push(host),
  });
  let output = "";
  let aborted = 0;
  const controller = new AbortController();
  const ctx = {
    cwd: root, toolCallId: "call-1", onData: (data: Buffer) => { output += data.toString(); },
    abort: () => { aborted += 1; }, signal: controller.signal,
  };
  return {
    root, seen, denials, amendments, hosts, orchestrator, ctx, firstRun,
    output: () => output,
    aborts: () => aborted,
    stop: () => controller.abort(),
    run: (command: string, extra: Record<string, unknown> = {}) =>
      orchestrator.runBash({ command, ...extra } as never, ctx),
  };
}

const allowEcho: PrefixRule = { pattern: ["echo"], decision: "allow" };
const allowSleep: PrefixRule = { pattern: ["sleep"], decision: "allow" };
const forbidden: PrefixRule = { pattern: ["curl"], decision: "forbidden", justification: "networking is off in this session" };

test("an allowed command runs without ever reaching a reviewer", async () => {
  const it = fixture({ script: () => ({ type: "approved" }), rules: [allowEcho] });
  expect((await it.run("echo hello")).exitCode).toBe(0);
  expect(it.output()).toContain("hello");
  expect(it.seen).toHaveLength(0);
});

test("a forbidden command is refused with its rule's justification and never runs", async () => {
  const it = fixture({ script: () => ({ type: "approved" }), rules: [forbidden] });
  await expect(it.run("curl https://example.com")).rejects.toThrow("networking is off in this session");
  expect(it.seen).toHaveLength(0);
  expect(it.denials).toHaveLength(1);
  expect(it.denials[0]!.rule).toBe("forbidden");
  expect(it.denials[0]!.action).toBe("bash: curl https://example.com");
  expect(it.denials[0]!.reason).toContain("networking is off in this session");
});

test("a prompt decision runs the command once the reviewer approves", async () => {
  const it = fixture({ script: () => ({ type: "approved" }) });
  const file = join(it.root, "written");
  expect((await it.run(`printf approved > ${JSON.stringify(file)}`)).exitCode).toBe(0);
  expect(readFileSync(file, "utf8")).toBe("approved");
  expect(it.seen).toHaveLength(1);
});

test("a refusal is the tool's error, is recorded, and leaves nothing behind", async () => {
  const it = fixture({ script: () => ({ type: "denied", rejection: userRejection }) });
  const file = join(it.root, "never");
  await expect(it.run(`printf denied > ${JSON.stringify(file)}`)).rejects.toThrow(userRejection);
  expect(existsSync(file)).toBe(false);
  expect(it.denials[0]!.rule).toBe("user");
});

test("an abort ends the turn and never runs the command", async () => {
  const it = fixture({ script: () => ({ type: "abort" }) });
  await expect(it.run("echo aborted")).rejects.toThrow(turnAbortedMessage);
  expect(it.aborts()).toBe(1);
  expect(it.output()).toBe("");
});

test("a guardian timeout is handed back as its own instructions, not as a denial", async () => {
  const it = fixture({ script: () => ({ type: "timed_out" }) });
  await expect(it.run("echo late")).rejects.toThrow("did not finish before its deadline");
  expect(it.denials[0]!.rule).toBe("guardian-timeout");
});

test("a prefix amendment is applied in memory and reported once", async () => {
  const it = fixture({ script: () => ({ type: "approved_execpolicy_amendment", prefix: ["git", "pull"] }) });
  expect((await it.run("git pull", { prefix_rule: ["git", "pull"] })).exitCode).not.toBe(null);
  expect(it.amendments).toEqual([{ pattern: ["git", "pull"] }]);
  await it.run("git pull");
  expect(it.seen).toHaveLength(1);
});

test("a network request with no running command is denied", async () => {
  const it = fixture({ script: () => ({ type: "approved" }) });
  expect(await it.orchestrator.decideNetwork(request("example.com"))).toEqual({ decision: "deny" });
});

test("the reviewer's network answers become the proxy's decision, once or for the session", async () => {
  const answers: ReviewDecision[] = [
    { type: "approved" },
    { type: "approved_for_session" },
    { type: "network_amendment", host: "example.com" },
  ];
  const it = fixture({ script: () => answers.shift() ?? { type: "denied", rejection: userRejection }, rules: [allowSleep] });
  const started = it.run("sleep 30");
  await it.firstRun;
  const decide = () => it.orchestrator.decideNetwork(request("example.com"));
  expect(await decide()).toEqual({ decision: "allow", scope: "once" });
  expect(await decide()).toEqual({ decision: "allow", scope: "session" });
  // The host is already allowed for the session, so nothing asks again.
  expect(await decide()).toEqual({ decision: "allow", scope: "session" });
  expect(it.seen).toHaveLength(2);
  it.stop();
  await started.catch(() => {});
});

test("a network amendment allows the host for the session and reports it", async () => {
  const it = fixture({ script: () => ({ type: "network_amendment", host: "api.example.com" }), rules: [allowSleep] });
  const started = it.run("sleep 30");
  await it.firstRun;
  expect(await it.orchestrator.decideNetwork(request("api.example.com")))
    .toEqual({ decision: "allow", scope: "session" });
  expect(it.hosts).toEqual(["api.example.com"]);
  it.stop();
  await started.catch(() => {});
});

test("a network denial stops the running command and becomes its tool result", async () => {
  const it = fixture({ script: () => ({ type: "denied", rejection: userRejection }), rules: [allowSleep] });
  const started = it.run("sleep 30");
  await it.firstRun;
  const decision = await it.orchestrator.decideNetwork(request("blocked.example.com"));
  expect(decision).toEqual({ decision: "deny" });
  await expect(started).rejects.toThrow(
    'Network access to "https://blocked.example.com:443" was blocked by policy.',
  );
  expect(it.denials.at(-1)!.rule).toBe("network");
});

test("under never, a network request is denied without asking anyone", async () => {
  const it = fixture({ script: () => ({ type: "approved" }), approvalPolicy: "never", rules: [allowSleep] });
  const started = it.run("sleep 30");
  await it.firstRun;
  expect(await it.orchestrator.decideNetwork(request("blocked.example.com"))).toEqual({ decision: "deny" });
  expect(it.seen).toHaveLength(0);
  await expect(started).rejects.toThrow("was blocked by policy");
});

test.skipIf(process.platform !== "darwin")(
  "a sandbox denial under untrusted asks once more and reruns without the sandbox",
  async () => {
    const outside = canonical(mkdtempSync(join(tmpdir(), "pi-approval-outside-")));
    roots.push(outside);
    const it = fixture({ script: () => ({ type: "approved" }), sandboxMode: "workspace-write" });
    const target = join(outside, "artifact");
    const result = await it.run(`printf built > ${JSON.stringify(target)}`);
    expect(result.exitCode).toBe(0);
    expect(readFileSync(target, "utf8")).toBe("built");
    expect(it.seen).toHaveLength(2);
    expect(it.seen[1]!.retryReason).toBe(retryWithoutSandboxReason);
  },
);

test.skipIf(process.platform !== "darwin")(
  "a refused retry hands the sandboxed failure back as it is",
  async () => {
    const outside = canonical(mkdtempSync(join(tmpdir(), "pi-approval-outside-")));
    roots.push(outside);
    const answers: ReviewDecision[] = [{ type: "approved" }, { type: "denied", rejection: userRejection }];
    const it = fixture({ script: () => answers.shift()!, sandboxMode: "workspace-write" });
    const result = await it.run(`printf built > ${JSON.stringify(join(outside, "artifact"))}`);
    expect(result.exitCode).not.toBe(0);
    expect(existsSync(join(outside, "artifact"))).toBe(false);
  },
);

function request(host: string): NetworkRequest {
  return { host, port: 443, protocol: "https_connect" };
}

/** The session as the suite builds it: a real security policy for the paths, and
 * the daemon's approval config for everything else. */
function session(overrides: Partial<ApprovalConfig>) {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-approval-session-")));
  roots.push(root);
  for (const name of ["project", "temp"]) mkdirSync(join(root, name));
  const configPath = join(root, "attn-security.json");
  writeFileSync(configPath, JSON.stringify({ enabled: true, network: "allow" }));
  const policy = resolveSecurityPolicy(loadSecurityConfig(configPath), join(root, "project"), configPath, join(root, "temp"));
  const approval = new PiApproval({
    config: { ...defaultApprovalConfig, ...overrides },
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
  const source = approval.sandboxSource();
  return { root, policy, spec: sandboxSpecFor(source.config, source.cwd, source.temp, { permissions: "use_default" }) };
}

test("the daemon's read-only mode wins over an enabled security policy", () => {
  const it = session({ sandboxMode: "read-only" });
  expect(it.policy.enabled).toBe(true);
  expect(it.spec).not.toBe("unsandboxed");
  if (it.spec === "unsandboxed") return;
  expect(it.spec.mode).toBe("read-only");
  expect(it.spec.writableRoots).toEqual([]);
  expect(it.spec.cwd).toBe(join(it.root, "project"));
});

test("workspace-write takes its writable roots from the security policy", () => {
  const it = session({ sandboxMode: "workspace-write" });
  expect(it.spec).not.toBe("unsandboxed");
  if (it.spec === "unsandboxed") return;
  expect(it.spec.writableRoots).toContain(join(it.root, "project"));
  expect(it.spec.writableRoots).toContain(join(it.root, "temp"));
});

test("the daemon's network switch decides the sandbox's network, not the security policy", () => {
  const off = session({ sandboxMode: "workspace-write", network: { ...defaultApprovalConfig.network, enabled: false } });
  expect(off.policy.network).toBe("allow");
  expect(off.spec === "unsandboxed" ? undefined : off.spec.network.enabled).toBe(false);
  const on = session({ sandboxMode: "workspace-write" });
  expect(on.spec === "unsandboxed" ? undefined : on.spec.network.enabled).toBe(true);
});

test("danger-full-access from the daemon runs unsandboxed however the policy is set", () => {
  expect(session({ sandboxMode: "danger-full-access" }).spec).toBe("unsandboxed");
});

/** A registered session answering its own approval cards, so the amendment travels
 * the path pi puts it on: user choice, orchestrator, suite report. */
async function registeredSession(reportExecPolicyAmendment: () => Promise<void>) {
  const root = canonical(mkdtempSync(join(tmpdir(), "pi-approval-amend-")));
  roots.push(root);
  const notices: { text: string; level: string }[] = [];
  const approval = new PiApproval({
    config: { ...defaultApprovalConfig, approvalPolicy: "untrusted", sandboxMode: "danger-full-access" },
    suite: {
      networkDecider: undefined,
      reportDenial: () => {},
      reportApprovalWindow: () => {},
      reportExecPolicyAmendment,
      reportNetworkAmendment: async () => {},
    },
    ledger: { record: () => {} },
  });
  approval.useSandbox({ cwd: root, temp: root, allowWrite: [root], denyRead: [], denyWrite: [], cacheWritePaths: [] });
  const handlers = new Map<string, (event: unknown, ctx: unknown) => unknown>();
  approval.register({
    registerFlag: () => {},
    registerCommand: () => {},
    getFlag: () => undefined,
    appendEntry: () => {},
    on: (name: string, handler: (event: unknown, ctx: unknown) => unknown) => handlers.set(name, handler),
  } as never);
  let output = "";
  const ctx = {
    cwd: root,
    toolCallId: "call-1",
    onData: (data: Buffer) => { output += data.toString(); },
    ui: {
      notify: (text: string, level: string) => { notices.push({ text, level }); },
      setStatus: () => {},
      select: async () => commandOptions.amendment("echo"),
    },
  };
  await handlers.get("session_start")!({}, ctx);
  return {
    notices,
    output: () => output,
    run: (command: string, extra: Record<string, unknown> = {}) =>
      approval.runBash({ command, ...extra } as never, ctx as never),
  };
}

test("an amendment attn never recorded is said out loud and still runs the command", async () => {
  const it = await registeredSession(async () => { throw new Error("suite relay connection closed"); });

  const result = await it.run("echo amended", { prefix_rule: ["echo"] });

  expect(result.exitCode).toBe(0);
  expect(it.output()).toContain("amended");
  expect(it.notices).toEqual([
    {
      text: "attn did not record this command amendment: suite relay connection closed. It holds for this session only.",
      level: "error",
    },
  ]);
});

test("an amendment attn recorded says nothing to the user", async () => {
  const it = await registeredSession(async () => {});

  expect((await it.run("echo amended", { prefix_rule: ["echo"] })).exitCode).toBe(0);
  expect(it.notices).toEqual([]);
});
