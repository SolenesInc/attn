import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { commandEnvironment } from "../sandbox/environment";
import { sandboxArgv } from "../sandbox/exec";
import type { SandboxMode } from "../sandbox/spec";
import type { CredentialFilter } from "./filter";
import { assertPath, type SecurityPolicy } from "./policy";
import { specForPolicy } from "./sandbox";

// Native tools use a small sandboxed worker so symlink races cannot bypass the OS policy.
const workerSource = `
const fs = require('node:fs/promises');
const readline = require('node:readline');
const input = readline.createInterface({input: process.stdin});
input.on('line', async line => {
  let request;
  try {
    request = JSON.parse(line);
    const {id, operation, path, content} = request;
    let value;
    switch (operation) {
      case 'read': value = (await fs.readFile(path)).toString('base64'); break;
      case 'write': await fs.writeFile(path, content); break;
      case 'mkdir': await fs.mkdir(path, {recursive: true}); break;
      case 'access': await fs.access(path); break;
      case 'stat': { const s = await fs.stat(path); value = {directory: s.isDirectory()}; break; }
      case 'readdir': value = await fs.readdir(path); break;
      default: throw new Error('Unknown filesystem operation');
    }
    process.stdout.write(JSON.stringify({id, value}) + '\\n');
  } catch (error) {
    process.stdout.write(JSON.stringify({id: request?.id, error: error.message}) + '\\n');
  }
});
`;

type Pending = { resolve: (value: unknown) => void; reject: (error: Error) => void };

export class SandboxedFilesystem {
  private child: ChildProcessWithoutNullStreams | undefined;
  private closed = false;
  private nextID = 0;
  private readonly pending = new Map<number, Pending>();

  constructor(readonly policy: SecurityPolicy, private readonly filter: CredentialFilter,
    private readonly sandboxMode: SandboxMode = "workspace-write") {}

  /** The session's permissions govern the native file tools too, the way Codex governs
   * apply_patch by the sandbox policy (core/src/safety.rs, assess_patch_safety). */
  assertWritable(path: string): string {
    if (this.sandboxMode !== "read-only") {
      return assertPath(this.policy, path, this.sandboxMode === "danger-full-access" ? "write-anywhere" : "write");
    }
    throw new Error("This session's permissions are read-only, so nothing was written. "
      + "Ask the user to change /permissions, or run the change as a bash command with "
      + "sandbox_permissions=require_escalated so a reviewer can approve it.");
  }

  async read(path: string): Promise<Buffer> {
    return Buffer.from(await this.request("read", path) as string, "base64");
  }

  async write(path: string, content: string): Promise<void> { await this.request("write", path, content); }
  async mkdir(path: string): Promise<void> { await this.request("mkdir", path); }
  async access(path: string): Promise<void> { await this.request("access", path); }
  async entries(path: string): Promise<string[]> { return await this.request("readdir", path) as string[]; }
  async directory(path: string): Promise<boolean> { return (await this.request("stat", path) as { directory: boolean }).directory; }

  /** Retires the instance for good: a call racing the rebuild that replaced it must not
   * bring this worker back under the permissions the session has already left. */
  close(): Promise<void> {
    this.closed = true;
    return this.abort();
  }

  /** Kills the worker so in-flight requests fail and the next call starts a fresh one.
   * A cancelled tool call ends here; only close() retires the instance. */
  abort(): Promise<void> {
    const child = this.child;
    this.child = undefined;
    this.fail(new Error("Security filesystem worker stopped"));
    if (!child) return Promise.resolve();
    return new Promise((resolve) => {
      child.once("close", () => resolve());
      child.kill("SIGTERM");
    });
  }

  private async request(operation: string, path: string, content?: string): Promise<unknown> {
    if (this.closed) {
      throw new Error("This session's file tools were rebuilt after a permissions change, so this call "
        + "did nothing. Run it again to use the tools this session has now.");
    }
    const target = operation === "write" || operation === "mkdir"
      ? this.assertWritable(path)
      : assertPath(this.policy, path, "read");
    const child = this.child ?? this.start();
    const id = ++this.nextID;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      child.stdin.write(`${JSON.stringify({ id, operation, path: target, content })}\n`, (error) => {
        if (error) { this.pending.delete(id); reject(new Error(this.filter.text(error.message))); }
      });
    });
  }

  private start(): ChildProcessWithoutNullStreams {
    // danger-full-access starts the worker unwrapped, giving up the symlink-race
    // protection the OS sandbox provides, exactly as bash gives it up under that mode.
    const spec = specForPolicy(this.policy, this.sandboxMode);
    const command = sandboxArgv(spec, process.execPath, ["-e", workerSource]);
    const child = spawn(command.executable, command.args, {
      cwd: this.policy.cwd,
      env: commandEnvironment(spec, this.filter.environment(process.env)),
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.child = child;
    let stderr = "";
    child.stderr.on("data", (data: Buffer) => { stderr = this.filter.text((stderr + data.toString()).slice(-4096)); });
    const reader = createInterface({ input: child.stdout });
    reader.on("line", (line) => {
      try {
        const message = JSON.parse(line);
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) pending?.reject(new Error(this.filter.text(message.error)));
        else pending?.resolve(message.value);
      } catch {
        this.abort();
      }
    });
    child.on("error", (error) => this.fail(new Error(`Security sandbox could not start: ${this.filter.text(error.message)}`)));
    child.on("close", (code) => {
      reader.close();
      if (this.child !== child) return;
      this.child = undefined;
      this.fail(new Error(`Security sandbox exited (${code}): ${stderr.trim()}`));
    });
    return child;
  }

  private fail(error: Error): void {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }
}
