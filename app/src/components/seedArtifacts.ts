import type { SeedArtifactReference, SeedNote } from '../types/generated';

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
