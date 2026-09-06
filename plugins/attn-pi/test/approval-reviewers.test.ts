import { expect, test } from "bun:test";
import { commandOptions, networkOptions, UserReviewer, userRejection, reviewTitle } from "../approval/reviewers";
import type { CommandApprovalRequest, NetworkApprovalRequest, ReviewUI } from "../approval/types";
import type { PrefixRule } from "../execpolicy/index";

function card(pick: (labels: string[]) => string | undefined) {
  const shown: string[][] = [];
  const titles: string[] = [];
  const waiting: boolean[] = [];
  let rules: PrefixRule[] = [];
  const ui: ReviewUI = {
    select: async (title, labels) => { titles.push(title); shown.push(labels); return pick(labels); },
    notify: () => {},
  };
  const reviewer = new UserReviewer({ rules: () => rules, onWaiting: (open) => waiting.push(open) });
  return {
    reviewer, shown, titles, waiting, ui,
    amend: (rule: PrefixRule) => { rules = [...rules, rule]; },
    review: (request: CommandApprovalRequest | NetworkApprovalRequest) => reviewer.review(request, { cwd: "/w", ui }),
  };
}

const command: CommandApprovalRequest = {
  kind: "command",
  command: "git push --force",
  cwd: "/w",
  sandboxPermissions: "use_default",
  prefixRule: ["git", "push"],
};

const network: NetworkApprovalRequest = {
  kind: "network", host: "example.com", port: 443, protocol: "https_connect",
  reason: "example.com is not in the allowed_domains",
};

test("the command card offers Codex's options in Codex's order", async () => {
  const it = card((labels) => labels[0]);
  expect(await it.review(command)).toEqual({ type: "approved" });
  expect(it.shown[0]).toEqual([
    commandOptions.approve,
    commandOptions.amendment("git push"),
    commandOptions.forSession,
    commandOptions.deny,
    commandOptions.abort,
  ]);
  expect(it.waiting).toEqual([true, false]);
});

test("each command option answers with its own decision", async () => {
  for (const [label, decision] of [
    [commandOptions.approve, { type: "approved" }],
    [commandOptions.amendment("git push"), { type: "approved_execpolicy_amendment", prefix: ["git", "push"] }],
    [commandOptions.forSession, { type: "approved_for_session" }],
    [commandOptions.deny, { type: "denied", rejection: userRejection }],
    [commandOptions.abort, { type: "abort" }],
  ] as const) {
    const it = card((labels) => labels.find((option) => option === label));
    expect(await it.review(command)).toEqual(decision);
  }
});

test("a command with no reusable prefix is not offered the amendment", async () => {
  const it = card((labels) => labels[0]);
  const { prefixRule: _prefix, ...bare } = command;
  await it.review(bare);
  expect(it.shown[0]).not.toContain(commandOptions.amendment("git push"));
});

test("each network option answers with its own decision", async () => {
  for (const [label, decision] of [
    [networkOptions.approve, { type: "approved" }],
    [networkOptions.forSession, { type: "approved_for_session" }],
    [networkOptions.amendment, { type: "network_amendment", host: "example.com" }],
    [networkOptions.deny, { type: "denied", rejection: userRejection }],
    [networkOptions.abort, { type: "abort" }],
  ] as const) {
    const it = card((labels) => labels.find((option) => option === label));
    expect(await it.review(network)).toEqual(decision);
  }
});

test("a dismissed card is not an approval", async () => {
  const it = card(() => undefined);
  expect(await it.review(command)).toEqual({ type: "denied", rejection: userRejection });
});

test("a session with no card at all refuses instead of running", async () => {
  const reviewer = new UserReviewer({ rules: () => [] });
  expect(await reviewer.review(command, { cwd: "/w" })).toEqual({ type: "denied", rejection: userRejection });
});

test("only a for-this-session answer is cached, and a rule change invalidates it", async () => {
  const it = card((labels) => labels.find((option) => option === commandOptions.forSession));
  expect(await it.review(command)).toEqual({ type: "approved_for_session" });
  expect(await it.review(command)).toEqual({ type: "approved" });
  expect(it.shown).toHaveLength(1);

  it.amend({ pattern: ["git", "push"], decision: "allow" });
  expect(await it.review(command)).toEqual({ type: "approved_for_session" });
  expect(it.shown).toHaveLength(2);
});

test("the cache is keyed by the command, its directory and its permissions", async () => {
  const it = card((labels) => labels.find((option) => option === commandOptions.forSession));
  await it.review(command);
  await it.review({ ...command, cwd: "/elsewhere" });
  await it.review({ ...command, sandboxPermissions: "require_escalated" });
  await it.review({ ...command, command: "git push --force-with-lease" });
  expect(it.shown).toHaveLength(4);
});

test("a plain approval is never cached", async () => {
  const it = card((labels) => labels[0]);
  await it.review(command);
  await it.review(command);
  expect(it.shown).toHaveLength(2);
});

test("the card shows the retry reason ahead of the policy reason", () => {
  const reason = reviewTitle({ ...command, reason: "not a known prefix", retryReason: "command failed; retry without sandbox?" });
  expect(reason).toContain("command failed; retry without sandbox?");
  expect(reason).not.toContain("not a known prefix");
  expect(reviewTitle({ ...command, justification: "the branch is mine" })).toContain("the branch is mine");
});
