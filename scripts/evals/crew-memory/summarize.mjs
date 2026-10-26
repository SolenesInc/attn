#!/usr/bin/env node
import { readFileSync, existsSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { resultEvent } from './eval.mjs';

const flow = resolve(process.argv[2]);
const rows = path => existsSync(path) ? readFileSync(path, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) : [];
const mean = values => values.reduce((sum, value) => sum + value, 0) / values.length;
const pct = value => value === null ? 'not measured' : (value * 100).toFixed(1) + '%';
const dollars = value => value === null ? 'not measured' : '$' + value.toFixed(4);

function wilson(score, n) {
  if (!n) return null;
  const z = 1.959963984540054;
  const denominator = 1 + z * z / n;
  const centre = (score + z * z / (2 * n)) / denominator;
  const radius = z * Math.sqrt(score * (1 - score) / n + z * z / (4 * n * n)) / denominator;
  return [centre - radius, centre + radius];
}

function caseScores(scored) {
  const byCase = new Map();
  for (const row of scored) {
    if (!Number.isFinite(row.grade?.pass)) throw new Error(`${row.prompt_id} has no pass score`);
    if (!byCase.has(row.prompt_id)) byCase.set(row.prompt_id, []);
    byCase.get(row.prompt_id).push(row.grade.pass);
  }
  const scores = [...byCase.values()].map(mean);
  const score = scores.length ? mean(scores) : null;
  return { cases: scores.length, reps: scored.length, score, interval: score === null ? null : wilson(score, scores.length) };
}

const variants = readdirSync(flow).filter(name => /^(baseline|v[1-9][0-9]*)$/.test(name));
const summary = [];
const lines = ['# Crew memory measurements', '', 'Intervals use unique cases as the independent units. Days and repeat judge votes do not increase that count.',
  'The letters-only baseline disables native harness memory in addition to attn memory. Production currently leaves Claude auto-memory on; parent F2 found it nearly unused.',
  'Pilot grades are exploratory until the user and judge have rated the same artifacts.', '',
  '| Variant | Unit | Cases | Scored reps | Pass, all | 95% interval | Headroom | Harness failures | Judge disagreements | Known spend | Fidelity flagged / unchecked reps | Checked faithful cases | Pass, checked faithful |',
  '| --- | --- | ---: | ---: | ---: | --- | ---: | ---: | --- | ---: | --- | ---: | ---: |'];

for (const variant of variants) {
  const results = rows(join(flow, variant, 'results.jsonl'));
  const errors = rows(join(flow, variant, 'errors.jsonl'));
  for (const unit of ['use', 'write', 'e2e']) {
    const scored = results.filter(row => row.tags?.[0] === unit && row.status === 'ok');
    const all = caseScores(scored);
    const { score, interval } = all;
    const faithful = caseScores(scored.filter(row => row.evaluation?.simulator_fidelity?.status === 'faithful'));
    const flagged = scored.filter(row => row.evaluation?.simulator_fidelity?.status === 'unfaithful').length;
    const unchecked = scored.length - faithful.reps - flagged;
    let disagreements = 0, votes = 0;
    for (const row of scored) {
      const [first, second] = row.evaluation?.judge_passes ?? [];
      if (!first || !second) continue;
      for (const verdict of first) {
        const repeat = second.find(item => item.id === verdict.id);
        if (!repeat) throw new Error(`${row.prompt_id} has incomplete repeated grading`);
        votes++;
        disagreements += verdict.yes !== repeat.yes ? 1 : 0;
      }
    }
    const failed = errors.filter(row => row.unit === unit);
    const costs = results.filter(row => row.tags?.[0] === unit).map(row => row.member_cost_usd + row.evaluation.calls_cost_usd);
    if (costs.some(value => !Number.isFinite(value) || value <= 0)) throw new Error(`${variant}/${unit} has an absent or implausible cost receipt`);
    const spend = costs.length ? costs.reduce((sum, value) => sum + value, 0) : null;
    let failedKnown = 0, incompleteSpend = scored.some(row => row.evaluation?.calls_spend_incomplete);
    for (const error of failed) {
      if (Number.isFinite(error.member_cost_usd)) failedKnown += error.member_cost_usd;
      else {
        for (const receipt of error.member_receipts ?? []) {
          if (Number.isFinite(receipt.usage.cost_usd)) failedKnown += receipt.usage.cost_usd;
          else incompleteSpend = true;
        }
        incompleteSpend = true;
      }
      for (const call of error.calls ?? []) {
        const response = resultEvent(call.response);
        if (Number.isFinite(response?.total_cost_usd)) failedKnown += response.total_cost_usd;
        else incompleteSpend = true;
      }
    }
    const knownSpend = spend === null && !failed.length ? null : (spend ?? 0) + failedKnown;
    const item = { variant, unit, ...all, headroom: score === null ? null : 1 - score,
      simulator_unfaithful_reps: flagged, simulator_unchecked_reps: unchecked,
      simulator_faithful: faithful,
      harness_failures: failed.length, truncated: results.filter(row => row.tags?.[0] === unit && row.status !== 'ok').length,
      judge_disagreements: disagreements, judge_claims_repeated: votes, successful_spend_usd: spend,
      failed_known_spend_usd: failedKnown, known_spend_usd: knownSpend, spend_incomplete: incompleteSpend,
      case_costs_usd: costs, seconds: scored.map(row => row.wall_clock_s) };
    summary.push(item);
    lines.push(`| ${variant} | ${unit} | ${all.cases} | ${all.reps} | ${pct(score)} | ${interval ? interval.map(pct).join(' to ') : 'not measured'} | ${pct(item.headroom)} | ${failed.length} | ${votes ? `${disagreements}/${votes}` : 'not measured'} | ${dollars(knownSpend)}${incompleteSpend ? ' + unknown spend' : ''} | ${flagged} / ${unchecked} | ${faithful.cases} | ${pct(faithful.score)} |`);
  }
}
lines.push('', 'Known spend includes member, simulator, both judge passes, the post-case fidelity check, and available receipts from failed attempts. Unknown spend is marked separately.',
  'All-case scores retain simulator-unfaithful outcomes. Checked-faithful scores exclude flagged cases and cases without a successful fidelity check, including older runs. Flagging is a model judgment for review, not a proven failure. Spend includes every recorded attempt regardless of fidelity.',
  'Intervals are Wilson intervals over case means, using the number of distinct cases. Additional reps do not pretend to add new histories.');
writeFileSync(join(flow, 'measurements.json'), JSON.stringify(summary, null, 2) + '\n');
writeFileSync(join(flow, 'measurements.md'), lines.join('\n') + '\n');
console.log(lines.join('\n'));
