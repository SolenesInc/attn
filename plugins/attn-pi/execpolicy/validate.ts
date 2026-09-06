// Rule self-tests. Port of codex-rs/execpolicy/src/parser.rs prefix_rule and
// rule.rs validate_match_examples / validate_not_match_examples.
import { CompiledPolicy, isDecision, shlexJoin } from "./policy";
import type { PatternToken, PrefixRule, RuleError } from "./types";

export function renderPattern(pattern: readonly PatternToken[]): string {
  return pattern
    .map((token) => (typeof token === "string" ? token : `[${token.join("|")}]`))
    .join(" ");
}

// Checks every rule and reports each failure with the rule's index, so settings
// can show a broken rule instead of dropping it.
export function validateRules(rules: readonly PrefixRule[]): RuleError[] {
  const errors: RuleError[] = [];
  rules.forEach((rule, index) => {
    const shape = shapeError(rule);
    if (shape !== undefined) {
      // A broken rule is reported by index and by pattern: the index addresses
      // it in settings, the pattern names it in a log line.
      const named = Array.isArray(rule.pattern) && rule.pattern.length > 0 ? renderPattern(rule.pattern) : undefined;
      errors.push({ index, message: named === undefined ? shape : `${shape} (rule \`${named}\`)` });
      return;
    }

    const pattern = renderPattern(rule.pattern);
    const policy = new CompiledPolicy([rule]);
    // not_match is checked first, as it is in parser.rs:145-148.
    for (const example of rule.not_match ?? []) {
      if (policy.matchesForCommand(example).length > 0) {
        errors.push({
          index,
          message: `expected example to not match rule \`${pattern}\`: ${shlexJoin(example)}`,
        });
        return;
      }
    }

    const unmatched = (rule.match ?? []).filter((example) => policy.matchesForCommand(example).length === 0);
    if (unmatched.length > 0) {
      const rendered = unmatched.map((example) => shlexJoin(example)).join(", ");
      errors.push({ index, message: `expected every example to match rule \`${pattern}\`: ${rendered}` });
    }
  });
  return errors;
}

function shapeError(rule: PrefixRule): string | undefined {
  if (!Array.isArray(rule.pattern) || rule.pattern.length === 0) return "invalid pattern element: pattern cannot be empty";
  for (const token of rule.pattern) {
    if (typeof token === "string") continue;
    if (!Array.isArray(token)) return "invalid pattern element: pattern element must be a string or list of strings";
    if (token.length === 0) return "invalid pattern element: pattern alternatives cannot be empty";
    if (token.some((alternative) => typeof alternative !== "string")) {
      return "invalid pattern element: pattern alternative must be a string";
    }
  }
  if (rule.decision !== undefined && !isDecision(rule.decision)) {
    return `invalid decision: ${rule.decision}`;
  }
  if (rule.justification !== undefined && rule.justification.trim() === "") {
    return "invalid rule: justification cannot be empty";
  }
  for (const field of ["match", "not_match"] as const) {
    const examples = rule[field];
    if (examples === undefined) continue;
    if (!Array.isArray(examples)) return `invalid example: ${field} must be a list of commands`;
    for (const example of examples) {
      if (!Array.isArray(example) || example.length === 0) return "invalid example: example cannot be an empty list";
      if (example.some((token) => typeof token !== "string")) return "invalid example: example tokens must be strings";
    }
  }
  return undefined;
}
