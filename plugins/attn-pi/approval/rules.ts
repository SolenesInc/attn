// The daemon's rule shape is the settings UI's; execpolicy's is Codex's. This is
// the one place the two meet, and where a rule that fails its own examples is dropped.
import { validateRules, type PrefixRule } from "../execpolicy/index";
import { renderPattern } from "../execpolicy/validate";
import type { ApprovalConfig, Rule } from "./config";

export type CompiledRules = { rules: PrefixRule[]; problems: string[] };

export function toPrefixRule(rule: Rule): PrefixRule {
  return {
    pattern: rule.pattern.map((alternatives) => (alternatives.length === 1 ? alternatives[0]! : [...alternatives])),
    decision: rule.decision,
    ...(rule.justification === "" ? {} : { justification: rule.justification }),
    ...(rule.match.length === 0 ? {} : { match: rule.match.map((example) => [...example]) }),
    ...(rule.notMatch.length === 0 ? {} : { not_match: rule.notMatch.map((example) => [...example]) }),
  };
}

/** A rule whose own examples fail is shown to the user and never matched: a silent
 * drop would let a rule the user believes in decide nothing. */
export function compileRules(config: ApprovalConfig): CompiledRules {
  const rules = config.rules.map(toPrefixRule);
  const errors = validateRules(rules);
  if (errors.length === 0) return { rules, problems: [] };
  const broken = new Set(errors.map((error) => error.index));
  return {
    rules: rules.filter((_rule, index) => !broken.has(index)),
    problems: errors.map((error) => `rule ${error.index + 1} (${describe(rules[error.index])}) is not in force: ${error.message}`),
  };
}

function describe(rule: PrefixRule | undefined): string {
  return rule === undefined ? "unknown" : renderPattern(rule.pattern);
}
