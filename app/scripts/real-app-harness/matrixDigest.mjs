// Kept out of run-serial-matrix.mjs: its `main()` runs at import time, so a
// test importing it would launch the actual matrix.

export function selectFailedScenarios(lastMatrixJson) {
  return (lastMatrixJson?.results || [])
    .filter((result) => result.code !== 0)
    .map((result) => result.id);
}

export function scenarioSkipReason(scenario, platform = process.platform, env = process.env) {
  const rule = scenario.skipOn?.[platform];
  if (!rule) {
    return null;
  }
  if (typeof rule === 'string') {
    return rule;
  }
  if (rule.unlessEnv && [rule.unlessEnv].flat().every(key => env[key])) {
    return null;
  }
  return rule.reason;
}

export function formatResultTable(results) {
  const idWidth = results.reduce((width, result) => Math.max(width, result.id.length), 2);
  return results
    .map((result) => {
      if (result.skipped) {
        return `SKIP  ${result.id.padEnd(idWidth)}  ${result.skipReason}`;
      }
      const status = result.code === 0 ? 'PASS' : 'FAIL';
      const seconds = (result.durationMs / 1000).toFixed(1);
      return `${status.padEnd(4)}  ${result.id.padEnd(idWidth)}  ${seconds}s`;
    })
    .join('\n');
}

export function formatShardDigest(shardResults, aggregate) {
  const shardLines = [...shardResults]
    .sort((a, b) => (a.shard?.index ?? 0) - (b.shard?.index ?? 0))
    .map((shard) => {
      const seconds = (shard.results || []).reduce((total, result) => total + (result.durationMs || 0), 0) / 1000;
      return `shard ${shard.shard?.index ?? '?'}/${shard.shard?.count ?? '?'}: `
        + `${(shard.results || []).length} scenarios, ${seconds.toFixed(1)}s (${shard.source})`;
    });
  const rows = formatResultTable(aggregate.results.map((result) => ({
    ...result,
    durationMs: result.durationMs ?? 0,
  }))).split('\n');
  const table = aggregate.results.map((result, index) => `s${result.shard}  ${rows[index]}`).join('\n');
  const failureSections = aggregate.failed
    .map((result) => `--- ${result.id} (shard ${result.shard}) ---\n${result.outputTail || '(no output captured)'}`)
    .join('\n\n');
  const problemSection = aggregate.problems.length > 0
    ? `\n\nShard coverage problems:\n${aggregate.problems.map((problem) => `  - ${problem}`).join('\n')}`
    : '';
  return { shardLines, digest: `${table}${problemSection}${failureSections ? `\n\n${failureSections}` : ''}\n` };
}
