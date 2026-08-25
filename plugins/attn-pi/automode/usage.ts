// pi sums `usage` on toolResult messages, but a BLOCKED call is answered inline and runs no
// result hook (pi 0.84.2), so a denial's tokens wait here and join the next one.

export type UsageLike = {
  input?: number;
  output?: number;
  cacheRead?: number;
  cacheWrite?: number;
  totalTokens?: number;
  cost?: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number; total?: number };
};

export class UsageLedger {
  private pending: UsageLike | undefined;

  add(usage: UsageLike | undefined): void {
    if (!usage) return;
    this.pending = mergeUsage(this.pending, usage);
  }

  drain(): UsageLike | undefined {
    const held = this.pending;
    this.pending = undefined;
    return held;
  }
}

export function mergeUsage(left: UsageLike | undefined, right: UsageLike | undefined): UsageLike | undefined {
  if (!left) return right;
  if (!right) return left;
  return {
    input: sum(left.input, right.input),
    output: sum(left.output, right.output),
    cacheRead: sum(left.cacheRead, right.cacheRead),
    cacheWrite: sum(left.cacheWrite, right.cacheWrite),
    totalTokens: sum(left.totalTokens, right.totalTokens),
    cost: {
      input: sum(left.cost?.input, right.cost?.input),
      output: sum(left.cost?.output, right.cost?.output),
      cacheRead: sum(left.cost?.cacheRead, right.cost?.cacheRead),
      cacheWrite: sum(left.cost?.cacheWrite, right.cost?.cacheWrite),
      total: sum(left.cost?.total, right.cost?.total),
    },
  };
}

function sum(left: number | undefined, right: number | undefined): number {
  return (left ?? 0) + (right ?? 0);
}
