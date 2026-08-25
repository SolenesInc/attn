// Kept out of run-serial-matrix.mjs: its `main()` runs at import time, so a
// test importing it would launch the actual matrix.

export function selectFailedScenarios(lastMatrixJson) {
  return (lastMatrixJson?.results || [])
    .filter((result) => result.code !== 0)
    .map((result) => result.id);
}

export function formatResultTable(results) {
  const idWidth = results.reduce((width, result) => Math.max(width, result.id.length), 2);
  return results
    .map((result) => {
      const status = result.code === 0 ? 'PASS' : 'FAIL';
      const seconds = (result.durationMs / 1000).toFixed(1);
      return `${status.padEnd(4)}  ${result.id.padEnd(idWidth)}  ${seconds}s`;
    })
    .join('\n');
}
