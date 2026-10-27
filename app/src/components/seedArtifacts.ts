import { invoke } from '@tauri-apps/api/core';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import type { SeedArtifact, SeedArtifactReference, SeedNote } from '../types/generated';

export type SeedDocumentNote = SeedNote;

export function artifactKey(artifact: SeedArtifactReference): string {
  return [
    artifact.kind,
    artifact.path ?? '',
    artifact.notebook_document_id ?? '',
    artifact.repository ?? '',
    artifact.url ?? '',
  ].join('\0');
}

/** The field that identifies an artifact — the part a person recognizes. */
export function artifactLabel(artifact: SeedArtifactReference): string {
  return artifact.path
    || artifact.notebook_document_id
    || artifact.url
    || artifact.repository
    || artifact.kind;
}

export async function openSeedArtifact(daemon: DaemonApi | null, seedId: string, artifact: SeedArtifact, reveal: boolean) {
  if (!daemon) throw new Error('The daemon is not connected');
  const target = await daemon.sendSeedArtifactTarget(seedId, artifact.relative_target, 'artifact');
  if (!target.path) throw new Error('The daemon returned no artifact path');
  await invoke('open_safe_seed_artifact_target', { path: target.path, reveal });
}
