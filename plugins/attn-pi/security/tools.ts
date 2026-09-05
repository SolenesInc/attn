import { securityPrompt } from "./guidance";
import {
  createBashToolDefinition, createEditToolDefinition, createFindToolDefinition, createGrepToolDefinition,
  createLocalBashOperations, createLsToolDefinition, createReadToolDefinition, createWriteToolDefinition,
  truncateHead, type BashOperations, type ToolDefinition,
} from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";
import { CredentialFilter, FilteredStream } from "./filter";
import { SandboxedFilesystem } from "./filesystem";
import { assertPath, type SecurityPolicy } from "./policy";
import { bashSandbox, shellQuote } from "./sandbox";
import { Type } from "typebox";
import { reviewUnavailable, sandboxRecovery, sandboxRequestSchema, scopedPolicy, type SandboxReview } from "./recovery";
import type { ToolExecutionCheck } from "../automode/index";

export function protectedBash(policy: SecurityPolicy, filter: CredentialFilter, reviewAvailable = () => false): BashOperations {
  const local = createLocalBashOperations({ shellPath: "/bin/bash" });
  return {
    async exec(command, cwd, options) {
      let permissionError = false;
      let networkError = false;
      let startupError = false;
      let tail = "";
      const stream = new FilteredStream(filter, (data) => {
        tail = (tail + data.toString()).slice(-4096);
        if (/permission denied|operation not permitted|read-only file system|\b(?:EACCES|EPERM|EROFS)\b/i.test(tail)) permissionError = true;
        if (/could not resolve|couldn't resolve|failed to (?:connect|fetch|download)|unable to (?:resolve|connect)|\b(?:ENOTFOUND|EAI_AGAIN|ENETUNREACH|ENETDOWN|ECONNREFUSED)\b|DNS.*(?:fail|error)/i.test(tail)) networkError = true;
        if (/sandbox-exec: sandbox_apply|bwrap:.*(?:namespace|mount|permission)/i.test(tail)) startupError = true;
        options.onData(data);
      });
      try {
        const result = await local.exec(bashSandbox(policy, command), cwd, {
          ...options,
          env: { ...filter.environment(options.env ?? process.env), TMPDIR: policy.temp, TMP: policy.temp, TEMP: policy.temp },
          onData: (data) => stream.write(data),
        });
        stream.finish();
        if (policy.enabled && result.exitCode !== 0 && (permissionError || (policy.network === "deny" && networkError))) {
          const guidance = startupError
            ? securityPrompt("startup-failure")
            : sandboxRecovery(policy, reviewAvailable(), permissionError ? "permission" : "network");
          options.onData(Buffer.from(`\n${filter.text(guidance)}\n`));
        }
        return result;
      } finally {
        stream.finish();
      }
    },
  };
}

export function protectedTools(policy: SecurityPolicy, filter: CredentialFilter, fs: SandboxedFilesystem, review?: SandboxReview, reviewAvailable = () => !!review, checkExecution?: ToolExecutionCheck): ToolDefinition[] {
  const bash = protectedBash(policy, filter, reviewAvailable);
  const bashTool = createBashToolDefinition(policy.cwd, { operations: bash, shellPath: "/bin/bash" });
  const parameters = Type.Object({ ...bashTool.parameters.properties, sandbox: Type.Optional(sandboxRequestSchema) });
  const reviewedBash: ToolDefinition<typeof parameters> = {
    ...bashTool,
    parameters,
    description: `${bashTool.description}\n${securityPrompt("bash-description")}`,
    promptGuidelines: [
      securityPrompt("bash-guidance"),
      securityPrompt("bash-credentials"),
    ],
    async execute(id, args, signal, onUpdate, ctx) {
      if (!Object.hasOwn(args, "sandbox")) return bashTool.execute(id, args, signal, onUpdate, ctx);
      if (!policy.enabled) throw new Error("The sandbox is disabled. Omit the sandbox request to run the command through normal auto-mode review.");
      if (!review || !reviewAvailable()) throw new Error(reviewUnavailable);
      const scoped = scopedPolicy(policy, args.sandbox!);
      const command = args.command;
      const timeout = args.timeout;
      const input = { command, ...(timeout === undefined ? {} : { timeout }), sandbox: scoped.request };
      const expected = JSON.stringify(input);
      const decision = await review({ type: "tool_call", toolCallId: id, toolName: "bash", input }, { ...ctx, cwd: policy.cwd, signal });
      if (signal?.aborted) throw new Error("Sandbox review cancelled; command was not run.");
      if (decision?.block) throw new Error(decision.reason ?? "Auto mode refused extra sandbox access. Explain the refusal to the user before retrying.");
      if (expected !== JSON.stringify(input) || JSON.stringify(scopedPolicy(policy, scoped.request).request) !== JSON.stringify(scoped.request)) {
        throw new Error("Sandbox request changed during review. Submit it again for review; the command was not run.");
      }
      const operations = protectedBash(scoped.policy, filter, reviewAvailable);
      const approved = filter.text(`Auto mode approved temporary sandbox access for this command: ${JSON.stringify(scoped.request)}\n`);
      return createBashToolDefinition(policy.cwd, { operations: {
        exec(command, cwd, options) {
          options.onData(Buffer.from(approved));
          return operations.exec(command, cwd, options);
        },
      }, shellPath: "/bin/bash" }).execute(id, { command, timeout }, signal, onUpdate, ctx);
    },
  };
  const rawRead = (path: string) => fs.read(path);
  const tools: ToolDefinition[] = [
    reviewedBash,
    createReadToolDefinition(policy.cwd, { operations: {
      readFile: async (path) => {
        const data = await rawRead(path);
        return imageType(data) ? data : Buffer.from(filter.text(data.toString("utf8"), true));
      },
      access: (path) => fs.access(path),
      detectImageMimeType: async (path) => imageType(await rawRead(path)),
    } }),
    createWriteToolDefinition(policy.cwd, { operations: {
      writeFile: (path, content) => fs.write(path, content), mkdir: (path) => fs.mkdir(path),
    } }),
    createEditToolDefinition(policy.cwd, { operations: {
      readFile: rawRead, writeFile: (path, content) => fs.write(path, content),
      access: async (path) => { assertPath(policy, path, "write", reviewAvailable()); await fs.access(path); },
    } }),
    createLsToolDefinition(policy.cwd, { operations: {
      exists: async (path) => { try { await fs.access(path); return true; } catch { return false; } },
      stat: async (path) => { const directory = await fs.directory(path); return { isDirectory: () => directory }; },
      readdir: (path) => fs.entries(path),
    } }),
  ];
  const find = createFindToolDefinition(policy.cwd);
  const protectedFind: typeof find = { ...find, async execute(_id, args, signal) {
    const path = assertPath(policy, args.path ?? policy.cwd, "read");
    const result = await capture(bash, ["rg", "--files", "--hidden", "--glob", "!.git/**", "--glob", args.pattern, "--", path], policy.cwd, signal);
    if (result.code !== 0 && result.code !== 1) throw new Error(result.text || `Sandboxed search failed (${result.code})`);
    const output = truncateHead(result.text || "No files found", { maxLines: args.limit ?? 1000 });
    return { content: [{ type: "text", text: output.content }], details: { truncation: output.truncated ? output : undefined } };
  } };
  tools.push(protectedFind);
  const grep = createGrepToolDefinition(policy.cwd);
  const protectedGrep: typeof grep = { ...grep, async execute(_id, args, signal) {
    const path = assertPath(policy, args.path ?? policy.cwd, "read");
    const command = ["rg", "--line-number", "--with-filename", "--color", "never", "--max-count", String(args.limit ?? 100)];
    if (args.ignoreCase) command.push("--ignore-case");
    if (args.literal) command.push("--fixed-strings");
    if (args.glob) command.push("--glob", args.glob);
    if (args.context !== undefined) command.push("--context", String(args.context));
    command.push("--", args.pattern, path);
    const result = await capture(bash, command, policy.cwd, signal);
    if (result.code !== 0 && result.code !== 1) throw new Error(result.text || `Sandboxed search failed (${result.code})`);
    const output = truncateHead(result.text || "No matches found", { maxLines: args.limit ?? 100 });
    return { content: [{ type: "text", text: output.content }], details: { truncation: output.truncated ? output : undefined } };
  } };
  tools.push(protectedGrep);
  return tools.map((tool) => ({ ...tool,
    renderCall: tool.name === "edit" ? (args, theme) => new Text(
      theme.fg("toolTitle", "edit") + " " + filter.text(String((args as { path?: string }).path ?? "")), 0, 0,
    ) : tool.renderCall ? (args, theme, context) => tool.renderCall!(filter.value(args), theme, context) : undefined,
    async execute(id, args, signal, onUpdate, ctx) {
    if (signal?.aborted) throw new Error("Aborted");
    const abort = () => fs.close();
    signal?.addEventListener("abort", abort, { once: true });
    try {
      args = structuredClone(args);
      checkExecution?.({ type: "tool_call", toolCallId: id, toolName: tool.name, input: args }, { ...ctx, cwd: policy.cwd, signal });
      return filter.value(await tool.execute(id, args, signal, onUpdate ? (update) => onUpdate(filter.value(update)) : undefined, ctx));
    } catch (error) {
      throw new Error(filter.text(error instanceof Error ? error.message : String(error)));
    } finally {
      signal?.removeEventListener("abort", abort);
    }
  } }));
}

async function capture(bash: BashOperations, args: string[], cwd: string, signal?: AbortSignal): Promise<{ text: string; code: number | null }> {
  let text = "";
  let overflow = false;
  const controller = new AbortController();
  const abort = () => controller.abort();
  if (signal?.aborted) abort();
  signal?.addEventListener("abort", abort, { once: true });
  try {
    const result = await bash.exec(args.map(shellQuote).join(" "), cwd, {
      signal: controller.signal,
      onData: (data) => {
        if (text.length + data.length > 1024 * 1024) { overflow = true; controller.abort(); }
        else text += data.toString();
      },
    });
    return { text, code: result.exitCode };
  } catch (error) {
    if (overflow) throw new Error("Search exceeded 1 MiB of output; narrow the path or pattern");
    throw error;
  } finally { signal?.removeEventListener("abort", abort); }
}

function imageType(data: Buffer): string | undefined {
  if (data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) return "image/png";
  if (data[0] === 255 && data[1] === 216 && data[2] === 255) return "image/jpeg";
  if (data[0] === 66 && data[1] === 77) return "image/bmp";
  if (/^GIF8[79]a/.test(data.subarray(0, 6).toString())) return "image/gif";
  if (data.subarray(0, 4).toString() === "RIFF" && data.subarray(8, 12).toString() === "WEBP") return "image/webp";
  return undefined;
}
