export function ResumeOnOutpostButton({ outpost, testId, unreachableReason, onResume }: {
  outpost: string;
  testId: string;
  unreachableReason?: string;
  onResume: () => void;
}) {
  const unreachable = unreachableReason !== undefined;
  return (
    <button
      type="button"
      className="garden-resume-split"
      data-testid={unreachable ? undefined : testId}
      aria-label={`Resume on ${outpost}`}
      disabled={unreachable}
      title={unreachableReason}
      onClick={onResume}
    >
      <span>Resume</span>
      <span className="garden-resume-split__machine">
        <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
          <rect x="2" y="2.5" width="12" height="4.5" rx="1" />
          <rect x="2" y="9" width="12" height="4.5" rx="1" />
          <circle cx="4.8" cy="4.75" r="0.6" fill="currentColor" />
          <circle cx="4.8" cy="11.25" r="0.6" fill="currentColor" />
        </svg>
        {outpost}
      </span>
    </button>
  );
}
