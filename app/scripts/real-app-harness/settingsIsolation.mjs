import fs from 'node:fs';
import { queryDaemonDb, writeDaemonSettings } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';

export function readStoredSettings(dbPath) {
  if (!fs.existsSync(dbPath)) return new Map();
  const rows = queryDaemonDb(dbPath, "SELECT key, value FROM settings WHERE copied_from = '' AND value != '';", { json: true });
  return new Map(rows.map(({ key, value }) => [key, value]));
}

export function storedSettingsChangedBetween(before, after) {
  return [...new Set([...before.keys(), ...after.keys()])]
    .filter((key) => before.get(key) !== after.get(key))
    .map((key) => ({ key, was: before.get(key) ?? '' }));
}

export function onlyWritableSettings(changes, listedSettings) {
  const writable = listedSettings.map(({ key }) => new RegExp(`^${key.split(/<[^>]+>/).map(escapeRegExp).join('.+')}$`));
  return changes.filter(({ key }) => writable.some((pattern) => pattern.test(key)));
}

export async function putBackStoredSettingsAndListLeaks(changes) {
  const shown = await settingsInEffect();
  await writeDaemonSettings(changes.map(({ key, was }) => ({ key, value: was })));
  const restored = await settingsInEffect();
  return changes
    .filter(({ key }) => shown.get(key) !== restored.get(key))
    .map(({ key }) => `${key}: ${restored.get(key) || 'unset'} -> ${shown.get(key) || 'unset'}`);
}

async function settingsInEffect() {
  const observer = new DaemonObserver();
  try {
    await observer.connect();
    return new Map(observer.settings);
  } finally {
    await observer.close();
  }
}

function escapeRegExp(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
