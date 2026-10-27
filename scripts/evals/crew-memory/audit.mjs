#!/usr/bin/env node
import { readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import { programmatic } from './eval.mjs';
import { Trial, save } from './trial.mjs';

const config = JSON.parse(readFileSync(resolve(process.argv[2]), 'utf8'));
const cases = JSON.parse(readFileSync(config.cases_path, 'utf8'));
const receipts = [];
const seen = new Set();
for (const input of cases) {
  if (seen.has(input.id.toLowerCase())) throw new Error(`duplicate case id: ${input.id}`);
  seen.add(input.id.toLowerCase());
  for (const source of input.sources) {
    const sha = createHash('sha256').update(source.text).digest('hex');
    if (sha !== source.sha256) throw new Error(`source hash mismatch: ${input.id}: ${source.path}`);
  }
  if (!input.sources?.length || (!input.checks?.length && !input.claims?.length)) throw new Error(`incomplete case ${input.id}`);
  const files = {};
  for (const check of input.checks ?? []) {
    let root = files[check.path] ? JSON.parse(files[check.path]) : {};
    let object = root;
    if (!(check.pointer?.length)) root = check.equals;
    else {
      for (const key of check.pointer.slice(0, -1)) object = object[key] ??= {};
      object[check.pointer.at(-1)] = check.equals;
    }
    files[check.path] = JSON.stringify(root);
  }
  const day_outputs = input.fact_ledger ? input.days.map((day, index) => ({ day: index + 1,
    files: { [day.output]: JSON.stringify(Object.fromEntries(day.required_fields.map(field => [field, 'synthetic present field']))) } })) : undefined;
  const oracle = programmatic(input, { files, day_outputs });
  const nullGrade = programmatic(input, { files: {} });
  if (Object.values(oracle).some(value => value !== 1) || Object.values(nullGrade).some(value => value !== 0)) throw new Error(`oracle/null failed for ${input.id}`);
  receipts.push({ case_id: input.id, unit: input.unit, source_hashes_valid: true,
    programmatic_claims: Object.keys(oracle).length, oracle, null: nullGrade,
    source_kind: 'agent letters recording user decisions; user verification pending' });
}

const trial = new Trial({ ...config, member_model: config.member_model }, new AbortController().signal);
let isolated;
try {
  await trial.start({ charter: '# Eval member\nHelp the user with local tasks.\n', letters: [] });
  isolated = { instance: trial.instance, isolation: trial.isolation, selection: trial.selection, cli_setup: 'crew set and resolved model/effort check succeeded', paid_calls: 0 };
} finally { await trial.stop(); }

const result = { at: new Date().toISOString(), cases: cases.length, receipts, isolated,
  pending: ['real member wake/work/sleep', 'judge oracle and empty/unknown/wrong-answer negatives',
    'induced provider error through full runner', 'user case approval', 'user/judge calibration',
    'pilot usage and cost', 'noise versus approved minimum useful win', 'memory ablation and build variance'] };
save(join(config.private_root, 'audit.json'), result);
const report = ['# Crew memory eval audit', '',
  'Partial audit. Paid execution and judge validity remain unverified.', '',
  `Measured: ${cases.length} cases have unique ids and matching frozen source hashes.`,
  `Measured: ${receipts.reduce((sum, item) => sum + item.programmatic_claims, 0)} programmatic outcome checks accept their oracle and reject absent output.`,
  'Measured: the real daemon starts in a disposable named instance, accepts WebSocket hello and crew settings, then cleans up.',
  'Measured: the containment policy denies private-file reads in a shell and its child, and denies connection to the production Unix socket.',
  'Measured: a sibling trial canary is unreadable, while the current trial remains writable.',
  '', 'These checks do not prove the full runner or model judge. Oracle/null coverage above enters the programmatic grader directly.',
  '', 'Pending before a full paid pass:', '', ...result.pending.map(item => '- ' + item),
  '', 'The case set uses agent-written letters as records of real user decisions. The user has not yet verified the proposed tasks or grading claims.',
  'Each trajectory is one case. Its days and the repeated judge votes are not independent observations.'];
writeFileSync(join(config.private_root, 'audit.md'), report.join('\n') + '\n');
console.log(report.join('\n'));
