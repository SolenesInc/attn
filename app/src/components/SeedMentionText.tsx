import type { SeedMentionIndex } from '../utils/seedMentions';

export function SeedMentionButton({ seedId, onOpenSeed, children }: {
  seedId: string;
  onOpenSeed: (seedId: string) => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className="seed-mention"
      data-seed-target={seedId}
      title={`Open ${seedId} in the Garden`}
      onClick={() => onOpenSeed(seedId)}
    >
      {children}
    </button>
  );
}

export function SeedMentionText({ text, index, onOpenSeed }: {
  text: string;
  index: SeedMentionIndex | null;
  onOpenSeed: (seedId: string) => void;
}) {
  if (!index) return <>{text}</>;
  return (
    <>
      {index.split(text).map((piece, position) => (
        typeof piece === 'string'
          ? piece
          : <SeedMentionButton key={position} seedId={piece.seedId} onOpenSeed={onOpenSeed}>{piece.text}</SeedMentionButton>
      ))}
    </>
  );
}
