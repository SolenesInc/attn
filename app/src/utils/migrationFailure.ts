import { invoke } from '@tauri-apps/api/core';

interface MigrationFailureMarkerFile {
  marker_path: string;
  contents: string;
}

export interface MigrationFailureFact {
  label: string;
  value: string;
}

export interface MigrationFailure {
  markerPath: string;
  facts: MigrationFailureFact[];
}

const KNOWN_FACTS: Array<[key: string, label: string]> = [
  ['daemon_log_path', 'Daemon log'],
  ['data_dir', 'Data directory'],
  ['database_path', 'Database'],
  ['backup_path', 'Backup'],
  ['schema_version_from', 'Schema version before'],
  ['schema_version_to', 'Schema version after'],
  ['error', 'Error'],
  ['binary_version', 'attn version'],
  ['failed_at', 'Failed at'],
];

function factValue(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value);
}

export function migrationFailureFromMarker(file: MigrationFailureMarkerFile): MigrationFailure {
  let parsed: unknown;
  try {
    parsed = JSON.parse(file.contents);
  } catch {
    parsed = null;
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return {
      markerPath: file.marker_path,
      facts: [{ label: 'Marker contents (not a JSON object)', value: file.contents }],
    };
  }
  const record = parsed as Record<string, unknown>;
  const known = new Set(KNOWN_FACTS.map(([key]) => key));
  const facts = [
    ...KNOWN_FACTS
      .filter(([key]) => key in record)
      .map(([key, label]) => ({ label, value: factValue(record[key]) })),
    ...Object.keys(record)
      .filter((key) => !known.has(key))
      .map((key) => ({ label: key, value: factValue(record[key]) })),
  ];
  return { markerPath: file.marker_path, facts };
}

export async function readMigrationFailureMarker(): Promise<MigrationFailure | null> {
  try {
    const file = await invoke<MigrationFailureMarkerFile | null>('read_migration_failure');
    return file ? migrationFailureFromMarker(file) : null;
  } catch (err) {
    console.error('[Daemon] Failed to read the migration failure marker:', err);
    return null;
  }
}
