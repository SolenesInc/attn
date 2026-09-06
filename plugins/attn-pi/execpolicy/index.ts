// Command evaluation. Port of codex-rs/core/src/exec_policy.rs
// create_exec_approval_requirement_for_parsed_commands (337-460) and
// render_decision_for_unmatched_command_for_platform (770-855).
import { dangerousCommandMatch, parseBashCommands, shellApprovalCommand, type DangerousCommandMatch } from "../shell/index";
import { CompiledPolicy, shlexJoin, strictestDecision, type PrefixMatch } from "./policy";
import type {
  CommandEvaluation,
  Decision,
  EvaluationInput,
  PrefixRule,
  RuleMatch,
} from "./types";

export type {
  ApprovalPolicy,
  CommandEvaluation,
  Decision,
  EvaluationInput,
  PatternToken,
  PrefixRule,
  RuleError,
  RuleMatch,
  SandboxMode,
  SandboxPermissions,
} from "./types";
export { amendRules, convertLegacyGlob } from "./amend";
export { validateRules } from "./validate";

// attn's bash tool always runs the script through this shell, so the argv the
// policy judges is fixed and the executable is always the configured shell.
const shellProgram = "bash";
const shellFlag = "-lc";

const promptConflictReason = "approval required by policy, but AskForApproval is set to Never";

export function evaluateCommand(command: string, input: EvaluationInput): CommandEvaluation {
  const argv = [shellProgram, shellFlag, command];
  const parsed = parseBashCommands(argv);
  const segmented = parsed !== undefined && parsed.length > 0;
  const commands: string[][] = segmented ? (parsed as string[][]) : [argv];

  // executable_identity.rs:43-46. An executable that is not the configured
  // shell is judged on its own, alongside the commands it appears to run.
  const executable = shellApprovalCommand(argv, shellProgram);
  if (executable.length !== argv.length) commands.unshift([...executable]);
  const renderedCommand = [...executable];

  const policy = new CompiledPolicy(input.rules);
  const matches: RuleMatch[] = [];
  const heuristics: { command: string[]; decision: Decision }[] = [];
  let everySegmentAllowed = true;
  for (const segment of commands) {
    const segmentMatches = policy.matchesForCommand(segment);
    if (segmentMatches.length === 0) {
      heuristics.push({ command: segment, decision: decisionForUnmatchedCommand(segment, input) });
      everySegmentAllowed = false;
      continue;
    }
    for (const match of segmentMatches) matches.push(toRuleMatch(match));
    if (!segmentMatches.some((match) => match.decision === "allow")) everySegmentAllowed = false;
  }

  const decision = strictestDecision([
    ...matches.map((match) => match.decision),
    ...heuristics.map((heuristic) => heuristic.decision),
  ]);
  const dangerous = commands.some((segment) => dangerousCommandMatch(segment) !== undefined);
  const evaluation = { commands, unparsed: !segmented, matches, dangerous };

  if (decision === "forbidden") {
    const reason = deriveForbiddenReason(renderedCommand, matches, dangerousHeuristic(heuristics, "forbidden"));
    return { ...evaluation, decision, bypassSandbox: false, reason };
  }

  if (decision === "prompt") {
    // Never has no reviewer to ask, so a prompt becomes a rejection
    // (exec_policy.rs:407-425). Under Never only a rule can ask for approval:
    // an unmatched dangerous command is already forbidden above.
    if (input.approvalPolicy === "never") {
      return { ...evaluation, decision: "forbidden", bypassSandbox: false, reason: promptConflictReason };
    }
    return { ...evaluation, decision, bypassSandbox: false, reason: derivePromptReason(renderedCommand, matches) };
  }

  return { ...evaluation, decision, bypassSandbox: everySegmentAllowed };
}

function toRuleMatch(match: PrefixMatch): RuleMatch {
  return { rule: match.rule, command: match.matchedPrefix, decision: match.decision };
}

function decisionForUnmatchedCommand(command: readonly string[], input: EvaluationInput): Decision {
  if (dangerousCommandMatch(command) !== undefined) {
    return input.approvalPolicy === "never" ? "forbidden" : "prompt";
  }
  switch (input.approvalPolicy) {
    case "never":
      return "allow";
    case "untrusted":
      return "prompt";
    case "on-request":
      // A restricted sandbox enforces the boundary on its own; only a request
      // to leave it needs a reviewer (exec_policy.rs:820-839).
      if (input.sandboxMode === "danger-full-access") return "allow";
      return input.sandboxPermissions === "require_escalated" ? "prompt" : "allow";
  }
}

function dangerousHeuristic(
  heuristics: readonly { command: string[]; decision: Decision }[],
  decision: Decision,
): DangerousCommandMatch | undefined {
  for (const heuristic of heuristics) {
    if (heuristic.decision !== decision) continue;
    const match = dangerousCommandMatch(heuristic.command);
    if (match !== undefined) return match;
  }
  return undefined;
}

function mostSpecificMatch(matches: readonly RuleMatch[], decision: Decision): RuleMatch | undefined {
  let best: RuleMatch | undefined;
  for (const match of matches) {
    if (match.decision !== decision) continue;
    // Ties keep the last match, as Rust's max_by_key does.
    if (best === undefined || match.command.length >= best.command.length) best = match;
  }
  return best;
}

function derivePromptReason(command: readonly string[], matches: readonly RuleMatch[]): string | undefined {
  const match = mostSpecificMatch(matches, "prompt");
  if (match === undefined) return undefined;
  const rendered = shlexJoin(command);
  const justification = ruleJustification(match.rule);
  return justification !== undefined
    ? `\`${rendered}\` requires approval: ${justification}`
    : `\`${rendered}\` requires approval by policy`;
}

function deriveForbiddenReason(
  command: readonly string[],
  matches: readonly RuleMatch[],
  dangerous: DangerousCommandMatch | undefined,
): string {
  const rendered = shlexJoin(command);
  const match = mostSpecificMatch(matches, "forbidden");
  if (match !== undefined) {
    const justification = ruleJustification(match.rule);
    if (justification !== undefined) return `\`${rendered}\` rejected: ${justification}`;
    return `\`${rendered}\` rejected: policy forbids commands starting with \`${shlexJoin(match.command)}\``;
  }
  if (dangerous !== undefined) return `\`${rendered}\` rejected: ${dangerousCommandRejectionReason(dangerous)}`;
  return `\`${rendered}\` rejected: blocked by policy`;
}

function dangerousCommandRejectionReason(dangerous: DangerousCommandMatch): string {
  return dangerous === "forced-rm"
    ? "rm -f style commands are not permitted. Use a safer approach"
    : "blocked by policy";
}

function ruleJustification(rule: PrefixRule): string | undefined {
  return rule.justification !== undefined && rule.justification.trim() !== "" ? rule.justification : undefined;
}
