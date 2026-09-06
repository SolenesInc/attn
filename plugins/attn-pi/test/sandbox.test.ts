import { describe, expect, spyOn, test } from "bun:test";
import * as childProcess from "node:child_process";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:net";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { CredentialFilter } from "../security/filter";
import { SandboxedFilesystem } from "../security/filesystem";
import { canonical, loadSecurityConfig, resolveSecurityPolicy } from "../security/policy";
import { protectedBash } from "../security/tools";
import { bwrapArgs } from "../sandbox/bwrap";
import { isSandboxDenial } from "../sandbox/denial";
import { commandEnvironment } from "../sandbox/environment";
import { wrapCommand } from "../sandbox/exec";
import { bashParameterSchema } from "../sandbox/schema";
import { sandboxSpecFor, type ProxyAddress, type SandboxConfig, type SandboxSpec } from "../sandbox/spec";

function workspace(): { cwd: string; temp: string; secret: string; outside: string; cleanup: () => void } {
  const root = canonical(mkdtempSync(join(tmpdir(), "attn-pi-sandbox-")));
  const cwd = join(root, "project");
  const temp = join(root, "temp");
  const secret = join(root, "secrets");
  const outside = join(root, "outside");
  for (const path of [cwd, temp, secret, outside]) mkdirSync(path);
  writeFileSync(join(secret, "key"), "s3cret\n");
  return { cwd, temp, secret, outside, cleanup: () => rmSync(root, { recursive: true, force: true }) };
}

function config(overrides: Partial<SandboxConfig> = {}): SandboxConfig {
  return { mode: "workspace-write", network: false, allowWrite: [], denyRead: [], denyWrite: [], cacheWritePaths: [], ...overrides };
}

function sandboxed(spec: SandboxSpec | "unsandboxed", command: string): { code: number | null; output: string } {
  if (spec === "unsandboxed") throw new Error("this fixture expects a sandboxed spec");
  const result = spawnSync("/bin/bash", ["--noprofile", "--norc", "-c", wrapCommand(spec, command)], { encoding: "utf8" });
  return { code: result.status, output: `${result.stdout}${result.stderr}` };
}

function listener(): Promise<Server & { port: number }> {
  return new Promise((resolve) => {
    const server = createServer((socket) => socket.end());
    server.listen(0, "127.0.0.1", () => resolve(Object.assign(server, { port: (server.address() as { port: number }).port })));
  });
}

describe("sandboxSpecFor", () => {
  test("workspace-write grants the cwd, the temp directory, /tmp, extra roots and build caches", () => {
    const { cwd, temp, outside, cleanup } = workspace();
    try {
      const spec = sandboxSpecFor(config({ allowWrite: [outside], cacheWritePaths: [join(outside, "cache")] }), cwd, temp, { permissions: "use_default" });
      expect(spec).not.toBe("unsandboxed");
      // The cache path is inside `outside`, so only the outermost root survives.
      expect((spec as SandboxSpec).writableRoots.slice().sort()).toEqual([cwd, temp, outside, canonical("/tmp")].sort());
    } finally { cleanup(); }
  });

  test("read-only grants no writable root", () => {
    const { cwd, temp, cleanup } = workspace();
    try {
      const spec = sandboxSpecFor(config({ mode: "read-only" }), cwd, temp, { permissions: "use_default" });
      expect((spec as SandboxSpec).writableRoots).toEqual([]);
    } finally { cleanup(); }
  });

  test("danger-full-access and require_escalated run unsandboxed", () => {
    const { cwd, temp, cleanup } = workspace();
    try {
      expect(sandboxSpecFor(config({ mode: "danger-full-access" }), cwd, temp, { permissions: "use_default" })).toBe("unsandboxed");
      expect(sandboxSpecFor(config(), cwd, temp, { permissions: "require_escalated" })).toBe("unsandboxed");
      expect(sandboxSpecFor(config({ mode: "read-only" }), cwd, temp, { permissions: "require_escalated" })).toBe("unsandboxed");
    } finally { cleanup(); }
  });

  test("the proxy is carried only while the network is enabled", () => {
    const { cwd, temp, cleanup } = workspace();
    const proxy: ProxyAddress = { host: "127.0.0.1", port: 4711, credentials: "run-token" };
    try {
      expect((sandboxSpecFor(config({ network: true }), cwd, temp, { permissions: "use_default", proxy }) as SandboxSpec).network)
        .toEqual({ enabled: true, proxy });
      expect((sandboxSpecFor(config({ network: false }), cwd, temp, { permissions: "use_default", proxy }) as SandboxSpec).network)
        .toEqual({ enabled: false });
    } finally { cleanup(); }
  });
});

describe.skipIf(process.platform !== "darwin")("seatbelt enforcement", () => {
  test("a write inside the workspace succeeds and a write outside it is denied", () => {
    const { cwd, temp, outside, cleanup } = workspace();
    try {
      const spec = sandboxSpecFor(config(), cwd, temp, { permissions: "use_default" });
      const allowed = sandboxed(spec, `echo inside > ${JSON.stringify(join(cwd, "ok"))}`);
      expect(allowed.output).toBe("");
      expect(allowed.code).toBe(0);
      expect(readFileSync(join(cwd, "ok"), "utf8")).toBe("inside\n");

      const denied = sandboxed(spec, `echo nope > ${JSON.stringify(join(outside, "blocked"))}`);
      expect(denied.code).not.toBe(0);
      expect(denied.output.toLowerCase()).toContain("operation not permitted");
      expect(existsSync(join(outside, "blocked"))).toBe(false);
      expect(isSandboxDenial({ sandboxed: true, exitCode: denied.code, output: denied.output })).toBe(true);
    } finally { cleanup(); }
  });

  test("the session temp directory is writable and TMPDIR points at it", () => {
    const { cwd, temp, cleanup } = workspace();
    try {
      const spec = sandboxSpecFor(config(), cwd, temp, { permissions: "use_default" }) as SandboxSpec;
      const env = commandEnvironment(spec, process.env);
      const result = spawnSync("/bin/bash", ["--noprofile", "--norc", "-c", wrapCommand(spec, 'echo scratch > "$TMPDIR/file"')], { encoding: "utf8", env });
      expect(`${result.stdout}${result.stderr}`).toBe("");
      expect(readFileSync(join(temp, "file"), "utf8")).toBe("scratch\n");
    } finally { cleanup(); }
  });

  test("workspace-write grants /tmp, read-only grants nothing, and neither reaches the home directory", () => {
    const { cwd, temp, cleanup } = workspace();
    const scratch = `/tmp/attn-pi-sandbox-${Math.random().toString(36).slice(2)}`;
    const home = join(homedir(), `attn-pi-sandbox-${Math.random().toString(36).slice(2)}`);
    try {
      const workspaceWrite = sandboxSpecFor(config(), cwd, temp, { permissions: "use_default" });
      expect(sandboxed(workspaceWrite, `echo hi > ${JSON.stringify(scratch)}`)).toEqual({ code: 0, output: "" });
      expect(readFileSync(scratch, "utf8")).toBe("hi\n");
      rmSync(scratch, { force: true });

      const readOnly = sandboxSpecFor(config({ mode: "read-only" }), cwd, temp, { permissions: "use_default" });
      expect(sandboxed(readOnly, `echo hi > ${JSON.stringify(scratch)}`).code).not.toBe(0);
      expect(existsSync(scratch)).toBe(false);

      for (const spec of [workspaceWrite, readOnly]) {
        expect(sandboxed(spec, `echo hi > ${JSON.stringify(home)}`).code).not.toBe(0);
        expect(existsSync(home)).toBe(false);
      }
    } finally {
      rmSync(scratch, { force: true });
      rmSync(home, { force: true });
      cleanup();
    }
  });

  test("read-only denies every write, including inside the cwd", () => {
    const { cwd, temp, cleanup } = workspace();
    try {
      const spec = sandboxSpecFor(config({ mode: "read-only" }), cwd, temp, { permissions: "use_default" });
      const denied = sandboxed(spec, `echo nope > ${JSON.stringify(join(cwd, "blocked"))}`);
      expect(denied.code).not.toBe(0);
      expect(existsSync(join(cwd, "blocked"))).toBe(false);
      expect(sandboxed(spec, `cat ${JSON.stringify(join(cwd, "..", "secrets", "key"))}`).output).toContain("s3cret");
    } finally { cleanup(); }
  });

  test("denyRead hides a path that is otherwise readable, and denyWrite protects a path inside the workspace", () => {
    const { cwd, temp, secret, cleanup } = workspace();
    const guarded = join(cwd, ".pi");
    mkdirSync(guarded);
    try {
      const spec = sandboxSpecFor(config({ denyRead: [secret], denyWrite: [guarded] }), cwd, temp, { permissions: "use_default" });
      const read = sandboxed(spec, `cat ${JSON.stringify(join(secret, "key"))}`);
      expect(read.code).not.toBe(0);
      expect(read.output).not.toContain("s3cret");

      const write = sandboxed(spec, `echo nope > ${JSON.stringify(join(guarded, "blocked"))}`);
      expect(write.code).not.toBe(0);
      expect(existsSync(join(guarded, "blocked"))).toBe(false);
      expect(sandboxed(spec, `echo fine > ${JSON.stringify(join(cwd, "sibling"))}`).code).toBe(0);
    } finally { cleanup(); }
  });

  test("a proxied profile reaches the proxy port and nothing else", async () => {
    const { cwd, temp, cleanup } = workspace();
    const proxy = await listener();
    const elsewhere = await listener();
    try {
      const spec = sandboxSpecFor(config({ network: true }), cwd, temp, {
        permissions: "use_default",
        proxy: { host: "127.0.0.1", port: proxy.port, credentials: "run-token" },
      });
      expect(sandboxed(spec, `exec 3<>/dev/tcp/127.0.0.1/${proxy.port}`).code).toBe(0);
      const blocked = sandboxed(spec, `exec 3<>/dev/tcp/127.0.0.1/${elsewhere.port}`);
      expect(blocked.code).not.toBe(0);
    } finally {
      proxy.close();
      elsewhere.close();
      cleanup();
    }
  });

  test("a profile without network reaches nothing", async () => {
    const { cwd, temp, cleanup } = workspace();
    const target = await listener();
    try {
      const spec = sandboxSpecFor(config(), cwd, temp, { permissions: "use_default" });
      expect(sandboxed(spec, `exec 3<>/dev/tcp/127.0.0.1/${target.port}`).code).not.toBe(0);
    } finally {
      target.close();
      cleanup();
    }
  });
});

describe.skipIf(process.platform !== "darwin")("spawned children", () => {
  test("the filesystem worker is spawned without the approval channel in its environment", async () => {
    const { cwd, temp, cleanup } = workspace();
    const settings = join(mkdtempSync(join(tmpdir(), "attn-pi-sandbox-settings-")), "attn-security.json");
    const policy = resolveSecurityPolicy(loadSecurityConfig(settings), cwd, settings, temp);
    writeFileSync(join(cwd, "note"), "worker-read");
    const fs = new SandboxedFilesystem(policy, new CredentialFilter({}));
    const saved = { ...process.env };
    process.env.ATTN_PI_TOKEN = "run-token";
    process.env.ATTN_PI_SUITE_SOCKET = "/tmp/attn-pi-suite.sock";
    const spy = spyOn(childProcess, "spawn");
    try {
      expect((await fs.read(join(cwd, "note"))).toString()).toBe("worker-read");
      const env = spy.mock.calls.at(-1)?.[2]?.env as Record<string, string>;
      expect(env.ATTN_PI_SUITE_SOCKET).toBeUndefined();
      expect(env.ATTN_PI_TOKEN).toBeUndefined();
      expect(Object.values(env)).not.toContain("/tmp/attn-pi-suite.sock");
      expect(env.TMPDIR).toBe(policy.temp);
    } finally {
      spy.mockRestore();
      await fs.close();
      process.env = saved;
      rmSync(dirname(settings), { recursive: true, force: true });
      cleanup();
    }
  });

  test("a sandboxed bash command cannot see the approval channel", async () => {
    const { cwd, temp, cleanup } = workspace();
    const settings = join(mkdtempSync(join(tmpdir(), "attn-pi-sandbox-settings-")), "attn-security.json");
    const policy = resolveSecurityPolicy(loadSecurityConfig(settings), cwd, settings, temp);
    let output = "";
    try {
      const result = await protectedBash(policy, new CredentialFilter({})).exec("env", cwd, {
        env: { ...process.env, ATTN_PI_TOKEN: "run-token", ATTN_PI_SUITE_SOCKET: "/tmp/attn-pi-suite.sock" },
        onData: (data) => { output += data.toString(); },
      });
      expect(result.exitCode).toBe(0);
      expect(output).toContain(`TMPDIR=${policy.temp}`);
      expect(output).not.toContain("ATTN_PI_SUITE_SOCKET");
      expect(output).not.toContain("run-token");
    } finally {
      rmSync(dirname(settings), { recursive: true, force: true });
      cleanup();
    }
  });
});

describe("bwrap arguments", () => {
  test("the workspace is bound writable, the protected path read-only, and the network namespace follows the profile", () => {
    const { cwd, temp, secret, cleanup } = workspace();
    const guarded = join(cwd, ".pi");
    mkdirSync(guarded);
    try {
      const offline = sandboxSpecFor(config({ denyRead: [secret], denyWrite: [guarded] }), cwd, temp, { permissions: "use_default" }) as SandboxSpec;
      const args = bwrapArgs(offline, ["/bin/bash", "-c", "true"]);
      expect(args.join(" ")).toContain(`--bind ${cwd} ${cwd}`);
      expect(args.join(" ")).toContain(`--ro-bind ${guarded} ${guarded}`);
      expect(args.join(" ")).toContain(`--tmpfs ${secret} --remount-ro ${secret}`);
      expect(args).toContain("--unshare-net");
      expect(args.slice(args.indexOf("--") + 1)).toEqual(["/bin/bash", "-c", "true"]);

      const online = sandboxSpecFor(config({ network: true }), cwd, temp, { permissions: "use_default" }) as SandboxSpec;
      expect(bwrapArgs(online, ["/bin/true"])).not.toContain("--unshare-net");
    } finally { cleanup(); }
  });
});

describe("commandEnvironment", () => {
  const spec = (proxy?: ProxyAddress): SandboxSpec => ({
    mode: "workspace-write", cwd: "/w", temp: "/w/tmp", writableRoots: ["/w"], denyRead: [], denyWrite: [],
    network: { enabled: !!proxy, ...(proxy ? { proxy } : {}) },
  });
  const inherited = { PATH: "/usr/bin", ATTN_PI_TOKEN: "run-token", ATTN_PI_SUITE_SOCKET: "/tmp/suite.sock", HTTP_PROXY: "http://old-token@127.0.0.1:1234" };

  test("the approval channel never reaches the command", () => {
    for (const target of [spec(), "unsandboxed" as const]) {
      const env = commandEnvironment(target, inherited);
      expect(env.ATTN_PI_TOKEN).toBeUndefined();
      expect(env.ATTN_PI_SUITE_SOCKET).toBeUndefined();
      expect(env.PATH).toBe("/usr/bin");
      expect(Object.values(env)).not.toContain("run-token");
    }
  });

  test("the proxy URL carries the credentials and the temp directory is redirected", () => {
    const env = commandEnvironment(spec({ host: "127.0.0.1", port: 8123, credentials: "run-token" }), inherited);
    expect(env.HTTP_PROXY).toBe("http://run-token@127.0.0.1:8123");
    expect(env.https_proxy).toBe("http://run-token@127.0.0.1:8123");
    expect(env.ALL_PROXY).toBe("socks5h://run-token@127.0.0.1:8123");
    expect(env.NO_PROXY).toBe("");
    expect(env.TMPDIR).toBe("/w/tmp");
    expect(env.TMP).toBe("/w/tmp");
    expect(env.TEMP).toBe("/w/tmp");
  });

  test("an inherited loopback proxy is dropped when this run has none, and escalation drops it too", () => {
    expect(commandEnvironment(spec(), inherited).HTTP_PROXY).toBeUndefined();
    const escalated = commandEnvironment("unsandboxed", { ...inherited, ALL_PROXY: "socks5h://run-token@127.0.0.1:8123" });
    expect(escalated.HTTP_PROXY).toBeUndefined();
    expect(escalated.ALL_PROXY).toBeUndefined();
    expect(escalated.TMPDIR).toBeUndefined();
  });

  test("the user's own proxy survives; only loopback proxies are ours to remove", () => {
    const corporate = { ...inherited, HTTP_PROXY: "http://proxy.corp:3128", NO_PROXY: "corp.internal" };
    expect(commandEnvironment(spec(), corporate).HTTP_PROXY).toBe("http://proxy.corp:3128");
    expect(commandEnvironment(spec(), corporate).NO_PROXY).toBe("corp.internal");
    expect(commandEnvironment(spec({ host: "127.0.0.1", port: 8123, credentials: "run-token" }), corporate).NO_PROXY).toBe("");
  });
});

describe("isSandboxDenial", () => {
  const result = (over: Partial<Parameters<typeof isSandboxDenial>[0]>) =>
    isSandboxDenial({ sandboxed: true, exitCode: 1, output: "", ...over });

  test("an unsandboxed run and a successful run are never denials", () => {
    expect(result({ sandboxed: false, output: "Operation not permitted" })).toBe(false);
    expect(result({ exitCode: 0, output: "Operation not permitted" })).toBe(false);
  });

  test("every denial keyword counts, in any case", () => {
    for (const output of ["Operation not permitted", "Permission denied", "Read-only file system", "SECCOMP failure", "sandbox-exec: deny", "landlock", "failed to write file"]) {
      expect(result({ output })).toBe(true);
    }
  });

  test("the quick-reject exit codes are not denials unless a keyword matched", () => {
    for (const exitCode of [2, 126, 127]) {
      expect(result({ exitCode, output: "command not found" })).toBe(false);
      expect(result({ exitCode, output: "read-only file system" })).toBe(true);
    }
  });

  test("an ordinary failure without a keyword is not a denial", () => {
    expect(result({ exitCode: 1, output: "fatal: not a git repository" })).toBe(false);
  });

  test("a SIGSYS kill is a denial", () => {
    expect(result({ exitCode: null, signal: "SIGSYS", output: "" })).toBe(true);
    expect(result({ exitCode: 128 + 31, output: "" })).toBe(process.platform === "linux");
  });
});

test("the bash schema offers Codex's escalation parameters and nothing more", () => {
  expect(Object.keys(bashParameterSchema.properties).sort())
    .toEqual(["command", "justification", "prefix_rule", "sandbox_permissions", "timeout"]);
  expect(JSON.stringify(bashParameterSchema)).not.toContain("with_additional_permissions");
  expect(bashParameterSchema.properties.sandbox_permissions.description)
    .toBe("Per-command sandbox override. Defaults to `use_default`; use `require_escalated` for unsandboxed execution.");
  expect(bashParameterSchema.required).toEqual(["command"]);
});
