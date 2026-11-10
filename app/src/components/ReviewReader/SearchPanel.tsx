import { useEffect, useRef, useState } from 'react';
import type { ReviewSearchResultMessage } from '../../types/generated';
import type { FileTarget } from './reviewContext';
import { PathLabel } from './StopBlock';

interface SearchResult {
  key: string;
  target: FileTarget;
  text?: string;
}

function resultsOf(answer: ReviewSearchResultMessage): SearchResult[] {
  return [
    ...(answer.files ?? []).map((path) => ({ key: `file:${path}`, target: { path } })),
    ...(answer.hits ?? []).map((hit) => ({ key: `hit:${hit.path}:${hit.line}`, target: { path: hit.path, line: hit.line }, text: hit.text })),
  ];
}

export function SearchPanel({
  initialQuery,
  search,
  onPick,
}: {
  initialQuery: string;
  search: (query: string) => Promise<ReviewSearchResultMessage>;
  onPick: (target: FileTarget) => void;
}) {
  const [query, setQuery] = useState(initialQuery);
  const [answer, setAnswer] = useState<{ query: string; results: SearchResult[]; problem?: string; error?: string } | null>(null);
  const [selected, setSelected] = useState(0);
  const latest = useRef(query);
  const listRef = useRef<HTMLUListElement | null>(null);

  useEffect(() => {
    latest.current = query;
    if (query.trim() === '') {
      setAnswer(null);
      return;
    }
    void search(query).then(
      (result) => { if (latest.current === query) setAnswer({ query, results: resultsOf(result), problem: result.problem }); },
      (error: unknown) => { if (latest.current === query) setAnswer({ query, results: [], error: error instanceof Error ? error.message : String(error) }); },
    );
  }, [query, search]);

  useEffect(() => { setSelected(0); }, [answer]);
  useEffect(() => {
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [selected]);

  const results = answer?.results ?? [];
  const move = (delta: number) => setSelected((at) => Math.max(0, Math.min(results.length - 1, at + delta)));

  return (
    <div className="review-search" role="dialog" aria-label="Search the repository">
      <input
        className="review-search-input"
        autoFocus
        value={query}
        placeholder="Search files by name and lines by text"
        aria-label="Search"
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'ArrowDown' || (event.ctrlKey && event.key === 'n')) { event.preventDefault(); move(1); }
          else if (event.key === 'ArrowUp' || (event.ctrlKey && event.key === 'p')) { event.preventDefault(); move(-1); }
          else if (event.key === 'Enter') { event.preventDefault(); const pick = results[selected]; if (pick) onPick(pick.target); }
        }}
      />
      {answer?.problem ? <div className="review-search-problem" role="status">{answer.problem}</div> : null}
      {answer?.error ? <div className="review-search-problem" role="alert">{answer.error}</div> : null}
      <ul className="review-search-results" ref={listRef} role="listbox">
        {results.map((result, index) => (
          <li
            key={result.key}
            role="option"
            aria-selected={index === selected}
            onMouseDown={(event) => { event.preventDefault(); onPick(result.target); }}
          >
            {result.text !== undefined ? (
              <>
                <span className="review-search-text">{result.text.trim()}</span>
                <span className="review-search-where">{result.target.path}:{result.target.line}</span>
              </>
            ) : (
              <>
                <PathLabel path={result.target.path} />
                <span className="review-search-where">file</span>
              </>
            )}
          </li>
        ))}
        {answer && results.length === 0 && !answer.error ? <li className="review-search-empty">Nothing matches {answer.query}</li> : null}
      </ul>
    </div>
  );
}
