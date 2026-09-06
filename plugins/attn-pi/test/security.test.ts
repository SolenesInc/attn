import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { CredentialFilter, FilteredStream, filteredLineLimit } from "../security/filter";
import { SandboxedFilesystem } from "../security/filesystem";
import { loadSecurityConfig, resolveSecurityPolicy, type SecurityPolicy } from "../security/policy";
import { protectedBash, protectedTools } from "../security/tools";
import { PiSecurity } from "../security/index";
import { createServer } from "node:net";
import { shellQuote } from "../security/sandbox";
import { visibleWidth } from "@earendil-works/pi-tui";
import { fixtureRoot } from "./fixture-root";

const directories: string[] = [];
const workers: SandboxedFilesystem[] = [];
afterEach(async () => {
  await Promise.all(workers.splice(0).map((worker) => worker.close()));
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

function fixture(): { root: string; policy: SecurityPolicy; fs: SandboxedFilesystem; filter: CredentialFilter } {
  const root = fixtureRoot("pi-security-test-");
  directories.push(root);
  for (const folder of ["project", "private", "scratch", "agent"]) mkdirSync(join(root, folder));
  const configPath = join(root, "agent", "attn-security.json");
  const config = loadSecurityConfig(configPath);
  config.buildCaches.enabled = false;
  config.denyRead = [join(root, "private")];
  config.denyWrite = [join(root, "private"), join(root, "agent")];
  writeFileSync(configPath, JSON.stringify(config));
  const policy = resolveSecurityPolicy(config, join(root, "project"), configPath, join(root, "scratch"));
  const filter = new CredentialFilter({ PROVIDER_API_KEY: "synthetic-provider-credential" });
  const fs = new SandboxedFilesystem(policy, filter);
  workers.push(fs);
  return { root, policy, fs, filter };
}

test("protected edit titles wrap long paths and filter credentials at terminal widths", () => {
  const { policy, filter, fs } = fixture();
  const tool = protectedTools(policy, filter, fs).find((tool) => tool.name === "edit")!;
  const path = `/outside/${"long-😀-directory/".repeat(12)}synthetic-provider-credential/file.txt`;
  const component = tool.renderCall!({ path }, { fg: (_color: string, text: string) => text } as never, {} as never)!;
  for (const width of [20, 36, 106]) {
    const lines = component.render(width);
    expect(lines.every((line) => visibleWidth(line) <= width)).toBe(true);
    expect(lines.join("")).not.toContain("synthetic-provider-credential");
    expect(lines.join("")).toContain("REDACTED");
  }
});

describe("credential filtering", () => {
  test("filters provider tokens and generic credential headers without relying on environment values", () => {
    const tokens = [
      `sk-proj-${"a".repeat(48)}`, `sk-ant-api03-${"b".repeat(93)}AA`,
      `AIza${"a".repeat(35)}`, `gsk_${"b".repeat(52)}`, `xai-${"c".repeat(80)}`,
      `ghp_${"a".repeat(36)}`, `github_pat_${"b".repeat(82)}`, `glpat-${"c".repeat(20)}`,
      `AKIA${"A".repeat(16)}`, `npm_${"a".repeat(36)}`, `rk_live_${"b".repeat(24)}`,
      `SG.${"a".repeat(22)}.${"b".repeat(43)}`, `SK${"a".repeat(32)}`, `key-${"b".repeat(32)}`,
      `xoxb-${"1".repeat(12)}-${"2".repeat(12)}-${"a".repeat(24)}`,
    ];
    const filter = new CredentialFilter();
    for (const token of tokens) expect(filter.text(`before ${token} after`)).toBe("before [REDACTED credential] after");
    expect(filter.text("x-service-identity: synthetic-opaque-value\nordinary output")).toBe("[REDACTED credential]\nordinary output");
    expect(filter.text('API_KEY="synthetic opaque value"\nordinary output')).toBe("[REDACTED credential]\nordinary output");
  });

  test("keeps provider authentication in the parent and strips child credentials and startup injection", () => {
    const env = { PROVIDER_API_KEY: "synthetic-secret", BASH_ENV: "/tmp/evil", NODE_OPTIONS: "--require=/tmp/evil", PATH: "/usr/bin", SSH_AUTH_SOCK: "/tmp/agent" };
    const filter = new CredentialFilter();
    expect(filter.environment(env)).toEqual({ PATH: "/usr/bin", SSH_AUTH_SOCK: "/tmp/agent" });
    expect(env.PROVIDER_API_KEY).toBe("synthetic-secret");
    expect(filter.text("got synthetic-secret")).not.toContain("synthetic-secret");
  });

  test("redacts whole multiline private keys, including an unterminated key", () => {
    const filter = new CredentialFilter();
    expect(filter.text("before\n-----BEGIN OPENSSH PRIVATE KEY-----\nfake-private-material\n-----END OPENSSH PRIVATE KEY-----\nafter")).toBe("before\n[REDACTED private key]\nafter");
    expect(filter.text("-----BEGIN PRIVATE KEY-----\nfake-private-material")).toBe("[REDACTED private key]");
  });

  test("redacts a token split at every possible byte boundary before emitting anything", () => {
    const token = `ghp_${"x".repeat(36)}`;
    const input = Buffer.from(`before ${token}\nafter 😀\n`);
    for (let split = 0; split <= input.length; split++) {
      let output = "";
      const stream = new FilteredStream(new CredentialFilter(), (part) => { output += part.toString(); });
      stream.write(input.subarray(0, split));
      stream.write(input.subarray(split));
      stream.finish();
      expect(output).toBe("before [REDACTED credential]\nafter 😀\n");
    }
  });

  test("streaming keys never expose body lines, and oversized lines are withheld", () => {
    let output = "";
    const stream = new FilteredStream(new CredentialFilter(), (part) => { output += part.toString(); });
    for (const line of ["-----BEGIN PRIVATE KEY-----\n", "fake-private-material\n", "-----END PRIVATE KEY-----\n", "safe\n", "x".repeat(filteredLineLimit + 1), "\ndone\n"]) stream.write(Buffer.from(line));
    stream.finish();
    expect(output).toBe("[REDACTED private key]\nsafe\n[REDACTED oversized output line]\ndone\n");
  });
});

describe.skipIf(!["darwin", "linux"].includes(process.platform))("actual OS enforcement", () => {
  test("native operations can work in the project and cannot write outside it or read denied data", async () => {
    const { root, fs } = fixture();
    writeFileSync(join(root, "private", "secret"), "synthetic-private-data");
    await fs.write(join(root, "project", "ok"), "hello");
    expect((await fs.read(join(root, "project", "ok"))).toString()).toBe("hello");
    await expect(fs.write(join(root, "outside"), "no")).rejects.toThrow("outside allowed");
    await expect(fs.read(join(root, "private", "secret"))).rejects.toThrow("blocked read");
  });

  test("an approved shell command still cannot cross write or read boundaries", async () => {
    const { root, policy, filter } = fixture();
    writeFileSync(join(root, "private", "secret"), "synthetic-private-data");
    let output = "";
    const bash = protectedBash(policy, filter);
    const result = await bash.exec(`printf hello > ok; cat '${root}/private/secret'; printf forbidden > '${root}/outside'`, policy.cwd, { onData: (part) => { output += part; } });
    expect(readFileSync(join(root, "project", "ok"), "utf8")).toBe("hello");
    expect(result.exitCode).not.toBe(0);
    expect(output).not.toContain("synthetic-private-data");
    expect(() => readFileSync(join(root, "outside"))).toThrow();
  });

  test("symlink writes are denied by the shell OS policy", async () => {
    const { root, policy, filter } = fixture();
    const link = join(root, "project", "escape");
    symlinkSync(join(root, "private"), link);
    const bash = protectedBash(policy, filter);
    const result = await bash.exec("printf nope > escape/secret", policy.cwd, { onData() {} });
    expect(result.exitCode).not.toBe(0);
    expect(() => readFileSync(join(root, "private", "secret"))).toThrow();
  });

  test("quoted paths stay literal and protected parents override nested write grants", async () => {
    const { root, policy, filter } = fixture();
    const directory = join(root, `cache 'quoted' "name"`);
    mkdirSync(directory);
    const restricted = { ...policy, allowWrite: [...policy.allowWrite, directory], denyWrite: [...policy.denyWrite, directory] };
    const command = `printf built > ${shellQuote(join(directory, "artifact"))}`;
    expect((await protectedBash({ ...policy, allowWrite: [...policy.allowWrite, directory] }, filter).exec(command, policy.cwd, { onData() {} })).exitCode).toBe(0);
    expect((await protectedBash(restricted, filter).exec(command, policy.cwd, { onData() {} })).exitCode).not.toBe(0);
    const nested = join(directory, "nested");
    mkdirSync(nested);
    const nestedPolicy = { ...restricted, allowWrite: [...restricted.allowWrite, nested] };
    const result = await protectedBash(nestedPolicy, filter).exec(`printf denied > ${shellQuote(join(nested, "artifact"))}`, policy.cwd, { onData() {} });
    expect(result.exitCode).not.toBe(0);
    expect(existsSync(join(nested, "artifact"))).toBe(false);
  });

  test("protects control paths without creating placeholder files in the project", async () => {
    const { policy, filter } = fixture();
    const bash = protectedBash(policy, filter);
    const result = await bash.exec("mkdir -p .pi .agents; echo forbidden > .pi/extension.js; echo forbidden > .agents/config", policy.cwd, { onData() {} });
    expect(result.exitCode).not.toBe(0);
    for (const name of [".pi", ".agents"]) {
      if (process.platform === "linux") expect(readdirSync(join(policy.cwd, name))).toEqual([]);
      else expect(existsSync(join(policy.cwd, name))).toBe(false);
    }
  });

  test("grants real worktree metadata but rejects an arbitrary gitdir pointer", async () => {
    const { root, policy, filter } = fixture();
    const git = (...args: string[]) => execFileSync("git", ["-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgSign=false", ...args], { cwd: policy.cwd, stdio: "pipe" });
    git("init", "-q");
    git("commit", "--allow-empty", "-qm", "initial");
    const worktree = join(root, "worktree");
    git("worktree", "add", "-qb", "security-test", worktree);
    const config = loadSecurityConfig(policy.configPath);
    const worktreePolicy = resolveSecurityPolicy(config, worktree, policy.configPath, policy.temp);
    const result = await protectedBash(worktreePolicy, filter).exec("echo change > file; git add file && git -c user.name=Test -c user.email=test@example.invalid -c commit.gpgSign=false commit -qm change", worktree, { onData() {} });
    expect(result.exitCode).toBe(0);
    writeFileSync(join(worktree, ".git"), `gitdir: ${root}\n`);
    const forged = resolveSecurityPolicy(config, worktree, policy.configPath, policy.temp);
    expect(forged.allowWrite).not.toContain(root);
    rmSync(join(worktree, ".git"));
    symlinkSync(root, join(worktree, ".git"));
    expect(resolveSecurityPolicy(config, worktree, policy.configPath, policy.temp).allowWrite).not.toContain(root);
  });

  test("the native worker denies an outside write even when its parent path check is disabled", async () => {
    const { root, policy, fs } = fixture();
    await fs.access(policy.cwd);
    policy.enabled = false;
    await expect(fs.write(join(root, "outside"), "no")).rejects.toThrow();
    expect(() => readFileSync(join(root, "outside"))).toThrow();
  });

  test("filters streamed shell output and overflow files before Pi can retain credentials", async () => {
    const { policy, filter, fs } = fixture();
    const bash = protectedTools(policy, filter, fs).find((tool) => tool.name === "bash")!;
    let streamed = "";
    const result = await bash.execute("test", { command: "printf 'synthetic-provider-'; printf 'credential\\n'; for i in {1..3000}; do echo ordinary-output; done" }, undefined, (part) => { streamed += JSON.stringify(part); }, undefined as never);
    expect(streamed).not.toContain("synthetic-provider-credential");
    expect(JSON.stringify(result)).not.toContain("synthetic-provider-credential");
    const details = result.details as { fullOutputPath?: string };
    expect(details.fullOutputPath).toBeDefined();
    const saved = readFileSync(details.fullOutputPath!, "utf8");
    expect(saved).toContain("REDACTED");
    expect(saved).not.toContain("synthetic-provider-credential");
    rmSync(details.fullOutputPath!);
  });

  test("search, edit, and a local Git workflow run under the same policy", async () => {
    const { root, policy, filter, fs } = fixture();
    const tools = protectedTools(policy, filter, fs);
    const run = (name: string, input: unknown) => tools.find((tool) => tool.name === name)!.execute("test", input, undefined, undefined, undefined as never);
    await run("write", { path: "sample.txt", content: "hello world\n" });
    await run("edit", { path: "sample.txt", edits: [{ oldText: "world", newText: "agent" }] });
    expect(JSON.stringify(await run("read", { path: "sample.txt" }))).toContain("hello agent");
    expect(JSON.stringify(await run("grep", { pattern: "hello", path: "." }))).toContain("hello agent");
    expect(JSON.stringify(await run("find", { pattern: "*.txt", path: "." }))).toContain("sample.txt");
    expect(JSON.stringify(await run("ls", { path: "." }))).toContain("sample.txt");
    await run("bash", { command: "git init -q && git add sample.txt && git -c user.name=Test -c user.email=test@example.invalid -c commit.gpgSign=false commit -qm initial && git status --porcelain" });
    expect(readFileSync(join(root, "project", "sample.txt"), "utf8")).toBe("hello agent\n");
  });

  test("build and test scripts can create and check project artifacts", async () => {
    const { policy, filter } = fixture();
    writeFileSync(join(policy.cwd, "build.cjs"), "const fs = require('fs'); fs.mkdirSync('dist'); fs.writeFileSync('dist/output', 'built');");
    writeFileSync(join(policy.cwd, "test.cjs"), "require('node:test')('build output', () => require('node:assert/strict').equal(require('fs').readFileSync('dist/output', 'utf8'), 'built'));");
    let output = "";
    const result = await protectedBash(policy, filter).exec("node build.cjs && node --test test.cjs", policy.cwd, { onData: (part) => { output += part; } });
    expect(result.exitCode).toBe(0);
    expect(output).toContain("build output");
    expect(readFileSync(join(policy.cwd, "dist", "output"), "utf8")).toBe("built");
  });

  test("redacting a private key preserves original read offsets", async () => {
    const { policy, filter, fs } = fixture();
    writeFileSync(join(policy.cwd, "key.txt"), "-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----\nordinary line four\n");
    const read = protectedTools(policy, filter, fs).find((tool) => tool.name === "read")!;
    const result = await read.execute("offset-test", { path: "key.txt", offset: 4, limit: 1 }, undefined, undefined, undefined as never);
    expect(JSON.stringify(result)).toContain("ordinary line four");
    expect(JSON.stringify(result)).not.toContain("private-material");
  });

  test("protected reads retain Pi's BMP image support", async () => {
    const { policy, filter, fs } = fixture();
    const bmp = Buffer.alloc(58);
    bmp.write("BM"); bmp.writeUInt32LE(58, 2); bmp.writeUInt32LE(54, 10);
    bmp.writeUInt32LE(40, 14); bmp.writeInt32LE(1, 18); bmp.writeInt32LE(1, 22);
    bmp.writeUInt16LE(1, 26); bmp.writeUInt16LE(24, 28); bmp.writeUInt32LE(4, 34);
    writeFileSync(join(policy.cwd, "pixel.bmp"), bmp);
    const read = protectedTools(policy, filter, fs).find((tool) => tool.name === "read")!;
    const result = await read.execute("image-test", { path: "pixel.bmp" }, undefined, undefined, undefined as never);
    expect(result.content.some((part) => part.type === "image")).toBe(true);
  });

  test("network deny blocks direct TCP rather than relying on proxy variables", async () => {
    const { policy, filter } = fixture();
    let connections = 0;
    const server = createServer((socket) => { connections++; socket.end(); });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    try {
      const port = (server.address() as { port: number }).port;
      const script = `const s=require('net').connect(${port},'127.0.0.1');s.on('connect',()=>{console.log('connected');s.destroy()});s.on('error',()=>console.log('blocked'));`;
      const command = `${shellQuote(process.execPath)} -e ${shellQuote(script)}`;
      let allowed = "";
      await protectedBash(policy, filter).exec(command, policy.cwd, { onData: (part) => { allowed += part; }, timeout: 5 });
      expect(allowed).toContain("connected");
      let denied = "";
      await protectedBash({ ...policy, network: "deny" }, filter).exec(command, policy.cwd, { onData: (part) => { denied += part; }, timeout: 5 });
      expect(denied).toContain("blocked");
      expect(connections).toBe(1);
    } finally { await new Promise<void>((resolve) => server.close(() => resolve())); }
  });
});

test("security configuration is independent of auto mode and malformed settings block all built-ins", async () => {
  const { policy } = fixture();
  const handlers = new Map<string, (event: any, ctx: any) => any>();
  const tools = new Map<string, any>();
  const commands = new Map<string, any>();
  const pi = { on: (name: string, fn: any) => handlers.set(name, fn), registerTool: (tool: any) => tools.set(tool.name, tool), registerCommand: (name: string, command: any) => commands.set(name, command) };
  const notices: string[] = [];
  const ctx = { cwd: policy.cwd, ui: { setStatus() {}, notify(text: string) { notices.push(text); } } };
  const security = new PiSecurity(policy.configPath);
  security.register(pi as never);
  try {
    await handlers.get("session_start")!({}, ctx);
    expect(tools.size).toBe(7);
    expect(commands.has("auto")).toBe(false);
    await commands.get("security").handler("off", ctx);
    expect(JSON.parse(readFileSync(policy.configPath, "utf8")).enabled).toBe(false);
    await commands.get("security").handler("on", ctx);
    expect(JSON.parse(readFileSync(policy.configPath, "utf8")).enabled).toBe(true);
    writeFileSync(policy.configPath, "malformed");
    await handlers.get("session_start")!({}, ctx);
    await expect(tools.get("bash").execute()).rejects.toThrow("Security configuration error");
    expect(notices.some((text) => text.includes("tools are blocked"))).toBe(true);
  } finally { await handlers.get("session_shutdown")!({}, ctx); }
});
