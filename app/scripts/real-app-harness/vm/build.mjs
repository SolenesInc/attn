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
    const profile = process.env.ATTN_PROFILE;
    if (!profile || !/^[a-z][a-z0-9-]{0,39}$/.test(profile) || ['default', 'prod', 'production'].includes(profile)) {
      throw new Error('Building a runner requires a named non-production ATTN_PROFILE');
    }
    run('make', ['install', `PROFILE=${profile}`]);
    ensureBundledPiInstalled();
  } catch (error) { console.error(error.message); process.exitCode = error.exitCode || 1; }
}
