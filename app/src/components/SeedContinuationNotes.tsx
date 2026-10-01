import type { Seed } from '../hooks/useDaemonSocket';

type Continuation = NonNullable<Seed['continuation']>;

function conversationLine(kept: NonNullable<Continuation['kept_conversation']>): string {
  if (kept.deleted_at) return `attn deleted its copy on ${kept.deleted_at.slice(0, 10)}`;
  const size = `kept by attn (${(kept.bytes / 1e6).toFixed(1)} MB)`;
  if (kept.delete_after) return `${size} until ${kept.delete_after.slice(0, 10)}; replant to keep it`;
  return `${size} while an open seed points at it`;
}

export function SeedContinuationNotes({ continuation, resumeOffered }: {
  continuation: Seed['continuation'];
  resumeOffered: boolean;
}) {
  return <>
    {continuation?.kept_conversation && (
      <p className="garden-head__reason">conversation{'  '}{conversationLine(continuation.kept_conversation)}</p>
    )}
    {!resumeOffered && continuation?.resume_reason && (
      <p className="garden-head__reason">{continuation.resume_reason}</p>
    )}
  </>;
}
