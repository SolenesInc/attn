// One pi session's approval machinery: the config, the two reviewers, the
// orchestrator behind the bash tool, and the /auto switch between them.
import { createLocalBashOperations, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { renderPrompt } from "../automode/prompt-catalog";
import { renderEnvironment } from "../automode/environment";
import type { DenialLedgerLike } from "../automode/ledger";
import { commandEnvironment, sandboxSpecFor, wrapCommand, type ProxyAddress, type SandboxConfig } from "../sandbox/index";
import type { Decider } from "../netproxy/index";
import { loadApprovalConfig, type ApprovalConfig, type RawApprovalConfig } from "./config";
import { GuardianReviewer, type GuardianUsageEntry } from "./guardian";
import { ApprovalOrchestrator, type OrchestratorDenial, type SandboxSource } from "./orchestrator";
import { compileRules } from "./rules";
import { UserReviewer } from "./reviewers";
import { transcriptFromSession, truncateForGuardian, maxToolEntryTokens } from "./transcript";
import type { Reviewer } from "./types";

export const approvalConfigEnvVar = "ATTN_PI_AUTOMODE_CONFIG";
export const statusKey = "attn-auto";

export type ApprovalSuiteLike = {
  networkDecider: Decider | undefined;
  reportDenial(denial: { tool: string; action: string; reason: string; rule: string; at: string }): void;
  reportApprovalWindow(open: boolean): void;
  reportExecPolicyAmendment(amendment: { pattern: string[]; decision: string; justification?: string }): Promise<void>;
  reportNetworkAmendment(amendment: { host: string; decision: string }): Promise<void>;
};

export type ApprovalSetup = {
  config: ApprovalConfig;
  suite: ApprovalSuiteLike;
  ledger: DenialLedgerLike;
  proxy?: ProxyAddress;
  /** The config the daemon sent could not be read; the user hears it once. */
  notice?: string;
};

export type ApprovalSource = { config: ApprovalConfig; problem?: string };

/** What the session's security policy contributes to the sandbox: the paths the
 * daemon's approval config does not carry. */
export type SandboxPaths = {
  cwd: string;
  temp: string;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  cacheWritePaths: string[];
};

export function attnApprovalSource(env: Record<string, string | undefined>): ApprovalSource | undefined {
  const raw = env[approvalConfigEnvVar]?.trim();
  if (!raw) return undefined;
  try {
    return { config: loadApprovalConfig(JSON.parse(raw) as RawApprovalConfig) };
  } catch (error) {
    return {
      config: loadApprovalConfig(undefined),
      problem: `attn sent an approval config this session could not read: ${message(error)}. ` +
        `Approvals are running on their shipped defaults.`,
    };
  }
}

export function proxyFromEnvironment(env: Record<string, string | undefined>): ProxyAddress | undefined {
  const address = env.ATTN_PI_PROXY_ADDR?.trim();
  const credentials = env.ATTN_PI_PROXY_CREDENTIALS?.trim();
  if (!address || !credentials) return undefined;
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  if (!Number.isInteger(port) || port <= 0) return undefined;
  return { host: "127.0.0.1", port, credentials };
}

export class PiApproval {
  private readonly orchestrator: ApprovalOrchestrator;
  private readonly user: UserReviewer;
  private guardian: GuardianReviewer | undefined;
  private paths: SandboxPaths | undefined;
  private choice: boolean | undefined;
  private flag: boolean | undefined;
  private context: ExtensionContext | undefined;
  private noticed = false;
  private readonly problems: string[];

  constructor(private readonly setup: ApprovalSetup) {
    const compiled = compileRules(setup.config);
    this.problems = compiled.problems;
    this.user = new UserReviewer({
      rules: () => this.orchestrator.rules(),
      onWaiting: (waiting) => setup.suite.reportApprovalWindow(waiting),
    });
    const local = createLocalBashOperations({ shellPath: "/bin/bash" });
    this.orchestrator = new ApprovalOrchestrator({
      approvalPolicy: () => setup.config.approvalPolicy,
      sandboxMode: () => setup.config.sandboxMode,
      sandbox: () => this.sandboxSource(),
      reviewer: () => this.reviewer(),
      rules: compiled.rules,
      ...(setup.proxy ? { proxy: setup.proxy } : {}),
      run: (command, cwd, options) => local.exec(command, cwd, options),
      onDenial: (denial) => this.record(denial),
      onExecPolicyAmendment: (pattern) =>
        this.amended("command", setup.suite.reportExecPolicyAmendment({ pattern, decision: "allow" })),
      onNetworkAmendment: (host) =>
        this.amended("host", setup.suite.reportNetworkAmendment({ host, decision: "allow" })),
      notify: (text, level) => this.context?.ui?.notify(text, level),
    });
  }

  /** PiSecurity owns the workspace paths and the session's temp directory; the
   * daemon's config owns what a command may do with them. */
  useSandbox(paths: SandboxPaths | undefined): void {
    this.paths = paths;
  }

  readonly runBash = (...args: Parameters<ApprovalOrchestrator["runBash"]>) => this.orchestrator.runBash(...args);

  enabled(): boolean {
    return this.choice ?? this.flag ?? this.setup.config.enabledDefault;
  }

  reviewer(): Reviewer {
    return this.enabled() && this.guardian ? this.guardian : this.user;
  }

  register(pi: ExtensionAPI): void {
    pi.registerFlag("auto", { description: "Start with attn auto mode on", type: "boolean" });
    pi.registerFlag("no-auto", { description: "Start with attn auto mode off", type: "boolean" });
    pi.registerCommand("auto", {
      description: "Toggle attn auto mode (on | off | status)",
      handler: (args, ctx) => this.command(args, ctx),
    });
    pi.on("session_start", async (_event, ctx) => {
      // --no-auto wins a session given both; an unset flag reads as undefined.
      this.flag = pi.getFlag("no-auto") === true ? false : pi.getFlag("auto") === true ? true : undefined;
      this.context = ctx;
      this.guardian = this.makeGuardian(pi, ctx);
      this.setup.suite.networkDecider = this.orchestrator.decideNetwork;
      this.paint(ctx);
      this.speak(ctx);
      await this.orchestrator.ready();
    });
    pi.on("agent_start", (_event, ctx) => {
      this.context = ctx;
      this.guardian?.startTurn();
    });
  }

  private makeGuardian(pi: ExtensionAPI, ctx: ExtensionContext): GuardianReviewer | undefined {
    const registry = ctx.modelRegistry;
    if (!registry) return undefined;
    const systemPrompt = renderPrompt(
      "system",
      { environment: renderEnvironment(this.setup.config.environment) },
      "pi-guardian",
    );
    return new GuardianReviewer({
      registry,
      model: () => this.context?.model,
      systemPrompt: () => systemPrompt,
      transcript: () => transcriptFromSession(this.context?.sessionManager.buildContextEntries() ?? []),
      sessionId: () => this.context?.sessionManager.getSessionId() ?? "",
      runTool: (command, signal) => this.inspect(command, signal),
      onUsage: (entry: GuardianUsageEntry) => pi.appendEntry("attn-guardian-usage", entry),
      notify: (text, level) => this.context?.ui?.notify(text, level),
    });
  }

  /** The Guardian looks with the agent's own sandbox, read-only and offline. */
  private async inspect(command: string, signal: AbortSignal): Promise<{ output: string; isError: boolean }> {
    const source = this.sandboxSource();
    const config: SandboxConfig = { ...source.config, mode: "read-only", network: false };
    const spec = sandboxSpecFor(config, source.cwd, source.temp, { permissions: "use_default" });
    const local = createLocalBashOperations({ shellPath: "/bin/bash" });
    let output = "";
    const result = await local.exec(spec === "unsandboxed" ? command : wrapCommand(spec, command), source.cwd, {
      onData: (data) => { output += data.toString(); },
      signal,
      env: commandEnvironment(spec, process.env),
    });
    return { output: truncateForGuardian(output, maxToolEntryTokens), isError: result.exitCode !== 0 };
  }

  /** The sandbox every command is wrapped with: the daemon's mode and network
   * over the security policy's paths. */
  sandboxSource(): SandboxSource {
    const paths = this.paths;
    if (!paths) throw new Error("security has not configured a sandbox for this session yet");
    // The daemon's sandbox_mode and network switch decide what a command may do:
    // the same values evaluateCommand already judged the command against.
    return {
      config: {
        mode: this.setup.config.sandboxMode,
        network: this.setup.config.network.enabled,
        allowWrite: paths.allowWrite,
        denyRead: paths.denyRead,
        denyWrite: paths.denyWrite,
        cacheWritePaths: paths.cacheWritePaths,
      },
      cwd: paths.cwd,
      temp: paths.temp,
    };
  }

  // The amendment already governs this session, and the command it approved runs
  // either way; only the "in the future" half is lost, and the user hears which.
  private amended(kind: string, reported: Promise<void>): void {
    void reported.catch((error: unknown) => {
      this.context?.ui?.notify(
        `attn did not record this ${kind} amendment: ${message(error)}. It holds for this session only.`,
        "error",
      );
    });
  }

  // The rejection already stands; a ledger or relay that cannot take it says so
  // out loud rather than turning a clean rejection into a failed tool call.
  private record(denial: OrchestratorDenial): void {
    try {
      this.setup.ledger.record(denial);
    } catch (error) {
      this.context?.ui?.notify(`This rejection has no local record: ${message(error)}`, "error");
    }
    try {
      this.setup.suite.reportDenial(denial);
    } catch (error) {
      this.context?.ui?.notify(`attn was not told about this rejection: ${message(error)}`, "error");
    }
  }

  private command(args: string, ctx: ExtensionContext): void {
    const asked = args.trim().toLowerCase();
    if (asked === "on") this.choice = true;
    else if (asked === "off") this.choice = false;
    else if (asked === "" || asked === "toggle") this.choice = !this.enabled();
    else if (asked !== "status") {
      ctx.ui?.notify(`/auto takes on, off, status, or nothing at all, not ${JSON.stringify(asked)}.`, "error");
      return;
    }
    this.paint(ctx);
    ctx.ui?.notify(this.status(), "info");
  }

  status(): string {
    if (this.enabled() && !this.guardian) {
      return "auto mode is off: this session has no model catalog for the automatic reviewer.";
    }
    return this.enabled()
      ? "auto mode is on: the automatic reviewer answers approvals, and asks you when it refuses."
      : "auto mode is off: you answer every approval yourself.";
  }

  private paint(ctx: ExtensionContext): void {
    ctx.ui?.setStatus(statusKey, `auto: ${this.enabled() && this.guardian ? "on" : "off"}`);
  }

  private speak(ctx: ExtensionContext): void {
    if (this.noticed || !ctx.ui) return;
    this.noticed = true;
    if (this.setup.notice !== undefined) ctx.ui.notify(this.setup.notice, "warning");
    for (const problem of this.problems) ctx.ui.notify(problem, "warning");
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
