import { run } from './providers.mjs';

try {
  const instance = process.env.ATTN_INSTANCE;
  if (!instance || !/^[a-z][a-z0-9-]{0,39}$/.test(instance) || ['default', 'prod', 'production'].includes(instance)) {
    throw new Error('Building a runner requires a named non-production ATTN_INSTANCE');
  }
  const app = process.argv[2] ?? 'tauri';
  if (!['tauri', 'native'].includes(app)) throw new Error('Building a runner takes the app tauri or native');
  run('make', ['install', `INSTANCE=${instance}`, `APP=${app}`]);
} catch (error) { console.error(error.message); process.exitCode = error.exitCode || 1; }
