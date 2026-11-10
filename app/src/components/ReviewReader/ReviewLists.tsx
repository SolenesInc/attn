import type { ReviewBundle, ReviewFile } from '../../types/generated';
import { useReviewReader } from './reviewContext';
import { PathLabel, StopBlock } from './StopBlock';

function shortCommit(commit: string | undefined): string {
  return commit ? commit.slice(0, 9) : '';
}

function FileRow({ file }: { file: ReviewFile }) {
  const reader = useReviewReader();
  return (
    <div
      className="review-file-row"
      data-review-cursor="file"
      data-cursor-key={`file:${file.path}`}
      data-path={file.path}
      onClick={() => reader?.openFile({ path: file.path })}
    >
      <span className={`review-file-status review-file-status--${file.status}`}>{file.status}</span>
      <PathLabel path={file.path} />
      {file.old_path ? <span className="review-file-from">from {file.old_path}</span> : null}
      <span className="review-file-stat">
        {file.binary ? 'binary' : (
          <>
            <span className="review-plus">+{file.added}</span> <span className="review-minus">−{file.removed}</span>
          </>
        )}
      </span>
    </div>
  );
}

export function ReviewSummary({ bundle }: { bundle: ReviewBundle }) {
  const added = bundle.files.reduce((sum, file) => sum + file.added, 0);
  const removed = bundle.files.reduce((sum, file) => sum + file.removed, 0);
  return (
    <section className="review-summary" aria-label="The change">
      <div className="review-summary-meta">
        <span className="review-kicker">Review</span>
        <span>{bundle.base ? <>base <code>{shortCommit(bundle.base)}</code> → </> : 'a tour at '}head <code>{shortCommit(bundle.head)}</code></span>
        <span>{bundle.files.length} {bundle.files.length === 1 ? 'file' : 'files'}</span>
        <span><span className="review-plus">+{added}</span> <span className="review-minus">−{removed}</span></span>
        <span>{bundle.stops.length} {bundle.stops.length === 1 ? 'stop' : 'stops'}</span>
      </div>
      {bundle.files.length > 0 ? (
        <div className="review-files">
          {bundle.files.map((file) => <FileRow key={file.path} file={file} />)}
        </div>
      ) : null}
      {bundle.problems.length > 0 ? (
        <ul className="review-problems" aria-label="Problems in the review document">
          {bundle.problems.map((problem, index) => (
            <li key={index}>{problem.line ? <span className="review-problem-line">line {problem.line}</span> : null}{problem.message}</li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

export function ReviewNotShown({ bundle }: { bundle: ReviewBundle }) {
  const reader = useReviewReader();
  return (
    <div className="review-not-shown">
      {bundle.not_shown.map((entry) => {
        const directory = entry.path.endsWith('/');
        return (
          <div
            key={entry.line}
            className="review-file-row"
            data-review-cursor="file"
            data-cursor-key={`not-shown:${entry.line}`}
            data-path={directory ? undefined : entry.path}
            data-search={directory ? entry.path : undefined}
            onClick={() => (directory ? reader?.openSearch(entry.path) : reader?.openFile({ path: entry.path }))}
          >
            <PathLabel path={entry.path} />
            <span className="review-not-shown-reason">{entry.reason}</span>
          </div>
        );
      })}
    </div>
  );
}

export function StopSlot({ line }: { line: number }) {
  const reader = useReviewReader();
  const stop = reader?.bundle.stops.find((candidate) => candidate.heading_line === line);
  return stop ? <StopBlock stop={stop} /> : null;
}

export function NotShownSlot() {
  const reader = useReviewReader();
  return reader ? <ReviewNotShown bundle={reader.bundle} /> : null;
}

export function SummarySlot() {
  const reader = useReviewReader();
  return reader ? <ReviewSummary bundle={reader.bundle} /> : null;
}
