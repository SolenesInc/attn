// Rule amendments. Port of codex-rs/execpolicy/src/amend.rs
// blocking_append_allow_prefix_rule, which appends and skips an exact duplicate.
import { ruleDecision } from "./policy";
import type { Decision, PatternToken, PrefixRule } from "./types";

export function amendRules(
  rules: readonly PrefixRule[],
  prefix: readonly string[],
  decision: Decision = "allow",
): PrefixRule[] {
  if (prefix.length === 0) throw new Error("execpolicy amendment needs at least one prefix token");
  const pattern = [...prefix];
  const duplicate = rules.some(
    (rule) => ruleDecision(rule) === decision && samePattern(rule.pattern, pattern),
  );
  if (duplicate) return [...rules];
  return [...rules, { pattern, decision }];
}

// Old auto mode matched a glob against the whole command line. Only a prefix of
// whole words survives as a rule; anything else stays a legacy pattern.
export function convertLegacyGlob(glob: string, decision: "allow" | "forbidden"): PrefixRule | undefined {
  const trimmed = glob.trim();
  if (trimmed === "" || trimmed.startsWith("*") || trimmed.startsWith("?")) return undefined;

  const words = trimmed.split(/\s+/);
  const pattern: string[] = [];
  for (const [index, word] of words.entries()) {
    if (word.includes("?")) return undefined;
    const star = word.indexOf("*");
    if (star === -1) {
      pattern.push(word);
      continue;
    }
    const trailing = index === words.length - 1 && star === word.length - 1;
    if (!trailing) return undefined;
    const head = word.slice(0, -1);
    if (head !== "") pattern.push(head);
  }

  if (pattern.length === 0) return undefined;
  return { pattern, decision };
}

function samePattern(left: readonly PatternToken[], right: readonly PatternToken[]): boolean {
  if (left.length !== right.length) return false;
  return left.every((token, index) => sameToken(token, right[index] as PatternToken));
}

function sameToken(left: PatternToken, right: PatternToken): boolean {
  if (typeof left === "string" || typeof right === "string") return left === right;
  return left.length === right.length && left.every((value, index) => value === right[index]);
}
