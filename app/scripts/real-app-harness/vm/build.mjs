import { pathToFileURL } from 'node:url';
import { run } from './providers.mjs';

export function ensureBundledPiInstalled(execute = run) {
  const catalog = JSON.parse(execute('./attn', ['plugin', 'list'], { stdio: ['ignore', 'pipe', 'inherit'] }));
  const pi = catalog.plugins.find((plugin) => plugin.name === 'attn-pi');
  if (pi?.availability === 'bundled' && pi.installation_state === 'installed') return;
  execute('./attn', ['plugin', 'install-bundled', 'attn-pi']);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const instance = process.env.ATTN_INSTANCE;
    if (!instance || !/^[a-z][a-z0-9-]{0,39}$/.test(instance) || ['default', 'prod', 'production'].includes(instance)) {
      throw new Error('Building a runner requires a named non-production ATTN_INSTANCE');
    }
    run('make', ['install', `INSTANCE=${instance}`]);
    ensureBundledPiInstalled();
  } catch (error) { console.error(error.message); process.exitCode = error.exitCode || 1; }
}
