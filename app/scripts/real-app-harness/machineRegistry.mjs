import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensureDir } from './common.mjs';

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_CANONICAL_PATH = path.join(MODULE_DIR, 'perf-baselines.json');
const DEFAULT_REGISTRY_DIR = path.join(os.homedir(), '.attn-perf-registry');

function readSysctl(key) {
  try {
    return execFileSync('sysctl', ['-n', key], { encoding: 'utf8' }).trim();
  } catch {
    return '';
  }
}

// The osRelease patch version is deliberately out of the key: a macOS point
// update must not orphan a machine's recorded baseline.
export function fingerprintKey({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor }) {
  const identity = JSON.stringify({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor });
  return crypto.createHash('sha256').update(identity).digest('hex').slice(0, 12);
}

export function getMachineFingerprint() {
  const arch = os.arch();
  const platform = os.platform();
  const osRelease = os.release();
  const hwModel = readSysctl('hw.model');
  const cpuBrand = readSysctl('machdep.cpu.brand_string') || os.cpus()[0]?.model || '';
  const cpuCount = os.cpus().length;
  const totalMemGb = Math.round(os.totalmem() / 1024 ** 3);
  const osMajor = parseInt(osRelease.split('.')[0], 10);

  const key = fingerprintKey({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor });

  return { key, arch, platform, osRelease, hwModel, cpuBrand, cpuCount, totalMemGb };
}

export function compareToBaseline(value, baselineValue, { tolerancePct = 10 } = {}) {
  if (baselineValue == null) {
    return { ok: true, value, baseline: null, deltaPct: null, tolerancePct, reason: 'no-baseline' };
  }

  const deltaPct = Math.round(((value - baselineValue) / baselineValue) * 1000) / 10;
  const ok = deltaPct <= tolerancePct;

  return { ok, value, baseline: baselineValue, deltaPct, tolerancePct, reason: ok ? 'within-band' : 'regression' };
}

export function loadBaseline(fingerprintKeyValue, { registryDir = DEFAULT_REGISTRY_DIR, canonicalPath = DEFAULT_CANONICAL_PATH } = {}) {
  const canonical = readJsonIfExists(canonicalPath);
  if (canonical && Object.prototype.hasOwnProperty.call(canonical, fingerprintKeyValue)) {
    return canonical[fingerprintKeyValue];
  }

  const localPath = path.join(registryDir, `${fingerprintKeyValue}.json`);
  return readJsonIfExists(localPath);
}

export function saveBaseline(fingerprintKeyValue, baseline, { registryDir = DEFAULT_REGISTRY_DIR } = {}) {
  ensureDir(registryDir);
  const localPath = path.join(registryDir, `${fingerprintKeyValue}.json`);
  fs.writeFileSync(localPath, `${JSON.stringify(baseline, null, 2)}\n`);
}

// `label` carries its own trailing space (e.g. 'cold '); it is concatenated,
// not joined.
export function recordOrCompareBaseline({ evaluation, key, label = '' }) {
  if (evaluation.baselineToSave) {
    saveBaseline(key, evaluation.baselineToSave);
    console.log(`[perf] recorded ${label}baseline for machine ${key}: ${evaluation.comparison.value} MB`);
  } else {
    console.log(
      `[perf] compared to ${label}baseline for machine ${key}: ${evaluation.comparison.value} MB `
      + `vs ${evaluation.comparison.baseline} MB (${evaluation.comparison.reason}, tolerance ${evaluation.comparison.tolerancePct}%)`,
    );
  }
}

function readJsonIfExists(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch {
    return null;
  }
}
