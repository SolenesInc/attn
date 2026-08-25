import { compareToBaseline } from './machineRegistry.mjs';
import { FIRST_FAILURE_MAX_LENGTH } from './common.mjs';

function truncateFirstFailure(message) {
  return message.length <= FIRST_FAILURE_MAX_LENGTH ? message : message.slice(0, FIRST_FAILURE_MAX_LENGTH);
}

// An explicit re-record passes by construction: this run defines the new
// baseline, so comparing it against the value it replaces would be pointless.
export function evaluateRssBaseline({ totalRssMb, fingerprint, baseline, tolerancePct = 15, record = false, recordedAt }) {
  const hasBaseline = baseline?.metrics?.totalRssMb != null;
  const comparison = record === true
    ? { ok: true, value: totalRssMb, baseline: null, deltaPct: null, tolerancePct, reason: 'recorded' }
    : compareToBaseline(totalRssMb, hasBaseline ? baseline.metrics.totalRssMb : null, { tolerancePct });
  const shouldRecord = record === true || !hasBaseline;
  const baselineToSave = shouldRecord
    ? { fingerprint, metrics: { totalRssMb }, recordedAt }
    : null;

  return { ok: comparison.ok, comparison, baselineToSave };
}

function buildVerdictEnvelope({ ok, failureCount, firstFailure, scenarioId, runId, artifactsDir, summaryPath, durationMs }) {
  return { ok, scenarioId, runId, failureCount, firstFailure, artifactsDir, summaryPath, durationMs };
}

export function buildBaselineVerdict({ ok, comparison, scenarioId, runId, artifactsDir, summaryPath, durationMs, extraMetrics = {} }) {
  const firstFailure = ok
    ? null
    : truncateFirstFailure(
        `RSS regression: ${comparison.value}MB vs baseline ${comparison.baseline}MB `
        + `(+${comparison.deltaPct}%, tolerance ${comparison.tolerancePct}%)`,
      );

  return {
    ...buildVerdictEnvelope({ ok, failureCount: ok ? 0 : 1, firstFailure, scenarioId, runId, artifactsDir, summaryPath, durationMs }),
    rss: comparison,
    metrics: { totalRssMb: comparison.value, ...extraMetrics },
  };
}

export function buildColdWarmVerdict({ cold, warm, scenarioId, runId, artifactsDir, summaryPath, durationMs }) {
  const ok = cold.ok && warm.ok;
  const failureCount = (cold.ok ? 0 : 1) + (warm.ok ? 0 : 1);
  const regressionLine = (label, c) =>
    `${label} RSS regression: ${c.value}MB vs baseline ${c.baseline}MB (+${c.deltaPct}%, tolerance ${c.tolerancePct}%)`;
  let firstFailure = null;
  if (!cold.ok) firstFailure = truncateFirstFailure(regressionLine('Cold', cold));
  else if (!warm.ok) firstFailure = truncateFirstFailure(regressionLine('Warm', warm));
  return {
    ...buildVerdictEnvelope({ ok, failureCount, firstFailure, scenarioId, runId, artifactsDir, summaryPath, durationMs }),
    rss: { cold, warm },
    metrics: { coldRssMb: cold.value, warmRssMb: warm.value },
  };
}

// Least-squares fit of `points` (y) against x = 0..n-1.
export function fitSlope(points) {
  if (points.length < 2) {
    throw new Error('fitSlope needs at least 2 points');
  }
  const n = points.length;
  let sumX = 0;
  let sumY = 0;
  let sumXY = 0;
  let sumXX = 0;
  for (let x = 0; x < n; x += 1) {
    const y = points[x];
    sumX += x;
    sumY += y;
    sumXY += x * y;
    sumXX += x * x;
  }
  const denominator = n * sumXX - sumX * sumX;
  const slope = (n * sumXY - sumX * sumY) / denominator;
  const intercept = (sumY - slope * sumX) / n;
  return { slope: Number(slope.toFixed(2)), intercept };
}

// `slope` is fitted by the caller, over the post-warmup portion of
// `retainedByCycle`.
export function buildLeakSoakVerdict({
  retainedByCycle,
  warmupCycles,
  slope,
  slopeThresholdMb,
  scenarioId,
  runId,
  artifactsDir,
  summaryPath,
  durationMs,
}) {
  const ok = slope <= slopeThresholdMb;
  const failureCount = ok ? 0 : 1;
  let firstFailure = null;
  if (!ok) {
    const post = retainedByCycle.slice(warmupCycles);
    const firstPost = post[0];
    const lastPost = post[post.length - 1];
    firstFailure = truncateFirstFailure(
      `Retained-RSS leak: slope ${slope}MB/cycle over ${retainedByCycle.length - warmupCycles} post-warmup cycles `
      + `exceeds ${slopeThresholdMb}MB/cycle (retained ${firstPost}->${lastPost}MB)`,
    );
  }
  return {
    ...buildVerdictEnvelope({ ok, failureCount, firstFailure, scenarioId, runId, artifactsDir, summaryPath, durationMs }),
    rss: { retainedByCycle, warmupCycles, slope, slopeThresholdMb },
    metrics: {
      retainedRssSlopeMbPerCycle: slope,
      firstRetainedMb: retainedByCycle[0],
      lastRetainedMb: retainedByCycle[retainedByCycle.length - 1],
    },
  };
}
