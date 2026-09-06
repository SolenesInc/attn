// Replays one recorded denial through the real Guardian, reading the ledger and
// writing nothing: bun run guardian-verdict.ts [timestamp-prefix] [provider/model]
import { ModelRegistry, ModelRuntime, createLocalBashOperations } from "@earendil-works/pi-coding-agent";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { GuardianReviewer } from "../approval/guardian";
import { renderPrompt } from "../automode/prompt-catalog";
import { emptyEnvironment, renderEnvironment } from "../automode/environment";
import { commandEnvironment, sandboxSpecFor, wrapCommand } from "../sandbox/index";
import { maxToolEntryTokens, truncateForGuardian } from "../approval/transcript";
import type { CommandApprovalRequest } from "../approval/types";

const [at = "2026-09-06T11:55:23", spec = "opencode-go/glm-5.3-flash"] = process.argv.slice(2);
const ledger = process.env.ATTN_DENIAL_LEDGER ?? join(process.env.HOME ?? "", ".attn", "attn-automode-denials.jsonl");
const record = readFileSync(ledger, "utf8").split("\n").filter(Boolean)
  .map((line) => JSON.parse(line) as { at: string; action: string; reason: string; rule: string; prompt?: { user: string } })
  .find((entry) => entry.at.startsWith(at));
if (!record) throw new Error(`no denial in ${ledger} at ${at}`);

const command = record.action.replace(/^bash:\s*/, "");
const cwd = /cd (\/[^\s&]+)/.exec(command)?.[1] ?? process.cwd();
const temp = mkdtempSync(join(tmpdir(), "guardian-receipt-"));

const runtime = await ModelRuntime.create();
const registry = new ModelRegistry(runtime);
await registry.refresh();
const [provider, ...rest] = spec.split("/");
const model = registry.find(provider!, rest.join("/"));
if (!model) throw new Error(`${spec} is not in this machine's model catalog`);

const local = createLocalBashOperations({ shellPath: "/bin/bash" });
const sandbox = sandboxSpecFor(
  { mode: "read-only", network: false, allowWrite: [], denyRead: [], denyWrite: [], cacheWritePaths: [] },
  cwd, temp, { permissions: "use_default" },
);

const request: CommandApprovalRequest = { kind: "command", command, cwd, sandboxPermissions: "use_default" };
const reviewer = new GuardianReviewer({
  registry: registry as never,
  model: () => model as never,
  systemPrompt: () => renderPrompt("system", { environment: renderEnvironment(emptyEnvironment) }, "pi-guardian"),
  transcript: () => [{ kind: "user", text: record.prompt?.user ?? record.reason }],
  sessionId: () => "receipt",
  runTool: async (input, signal) => {
    let output = "";
    const result = await local.exec(sandbox === "unsandboxed" ? input : wrapCommand(sandbox, input), cwd, {
      onData: (data) => { output += data.toString(); }, signal, env: commandEnvironment(sandbox, process.env),
    });
    console.log(`  tool: ${input} -> exit ${result.exitCode}`);
    return { output: truncateForGuardian(output, maxToolEntryTokens), isError: result.exitCode !== 0 };
  },
  onUsage: (usage) => console.log(`  usage: ${JSON.stringify(usage.usage)} outcome=${usage.outcome}`),
  notify: (text) => console.log(`  ${text}`),
});

console.log(`replaying ${record.at} (${record.rule}) with ${spec}`);
console.log(`command: ${command}`);
console.log(`classifier said: ${record.reason}`);
const decision = await reviewer.review(request, { cwd });
console.log(`guardian said: ${JSON.stringify(decision, undefined, 2)}`);
