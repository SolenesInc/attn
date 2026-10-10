import type { Seed } from '../hooks/useDaemonSocket';

type Continuation = NonNullable<Seed['continuation']>;

function conversationLine(kept: NonNullable<Continuation['kept_conversation']>): string {
  if (kept.deleted_at) return `${kept.deleted_by?.ref === 'user' ? 'you deleted attn’s copy' : 'attn deleted its copy'} on ${kept.deleted_at.slice(0, 10)}`;
  const bytes = kept.bytes;
  const amount = bytes >= 1e6 ? `${(bytes / 1e6).toFixed(1)} MB`
    : bytes >= 1e3 ? `${(bytes / 1e3).toFixed(1)} KB` : `${bytes} B`;
  const size = `kept by attn (${amount})`;
  if (kept.pinned_at) return `${size} forever; pinned`;
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
