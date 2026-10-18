import { Fragment } from 'react';

export type WhereRow =
  | { kind: 'new'; from: 'default' | 'current'; branch: string }
  | { kind: 'main'; branch: string }
  | { kind: 'existing'; path: string; branch: string; createdAt?: string };

export function locationAge(timestamp?: string) {
  if (!timestamp) return '';
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(timestamp)) / 60000));
  if (minutes < 1) return 'now';
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return hours < 24 ? `${hours}h` : `${Math.floor(hours / 24)}d`;
}

export function WhereList({
  rows,
  highlight,
  name,
  typed,
  repo,
  displayPath,
  remembered,
  onPick,
  deletion,
}: {
  deletion: { path: string; branch: string; force: boolean } | null;
  rows: WhereRow[];
  highlight: number;
  name: string;
  typed: boolean;
  repo: string;
  displayPath: (path: string) => string;
  remembered: 'main_repo' | 'new_worktree';
  onPick: (index: number) => void;
}) {
  return (
    <div className="picker-results" role="listbox" aria-label="Where">
      {rows.map((row, index) => (
        <Fragment key={row.kind === 'existing' ? row.path : `${row.kind}:${row.branch}`}>
          {(index === 0 || (rows[index - 1].kind === 'new' && row.kind !== 'new')) && (
            <div className="picker-section-title">
              {row.kind === 'new' ? (
                <>
                  New worktree · <span className="launcher-branch">{name}</span>
                  {!typed && <span className="launcher-section-hint"> · type to rename</span>}
                </>
              ) : (
                'Open existing'
              )}
            </div>
          )}
          <div
            role="option"
            aria-selected={index === highlight}
            tabIndex={-1}
            className={`picker-item${index === highlight ? ' selected' : ''}`}
            data-path={row.kind === 'existing' ? row.path : repo}
            data-index={index}
            data-testid={`launcher-where-item-${index}`}
            data-kind={row.kind}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => onPick(index)}
          >
            <span className="launcher-icon">
              {row.kind === 'existing' && deletion?.path === row.path
                ? '⌫'
                : row.kind === 'new'
                  ? '⎇'
                  : row.kind === 'main'
                    ? '●'
                    : '◎'}
            </span>
            <div className="picker-info">
              <div className="picker-name">
                {row.kind === 'existing' && deletion?.path === row.path
                  ? deletion.force
                    ? `${row.branch} has uncommitted changes`
                    : `Delete ${row.branch}?`
                  : row.kind === 'new'
                    ? `from ${row.branch}`
                    : row.kind === 'main'
                      ? 'main checkout'
                      : row.branch}
              </div>
              <div className="picker-path">
                {row.kind === 'existing' && deletion?.path === row.path
                  ? deletion.force
                    ? 'Force delete local worktree and branch? (y/n)'
                    : `${displayPath(row.path)} (y/n)`
                  : row.kind === 'new'
                    ? row.from === 'default'
                      ? 'the default branch'
                      : 'the branch the main checkout is on'
                    : row.kind === 'main'
                      ? `${displayPath(repo)} · on ${row.branch}`
                      : displayPath(row.path)}
              </div>
            </div>
            {((row.kind === 'main' && remembered === 'main_repo') ||
              (row.kind === 'new' &&
                row.from === 'default' &&
                remembered === 'new_worktree')) && (
              <span className="launcher-badge">last used</span>
            )}
            {row.kind === 'existing' &&
              (deletion?.path === row.path ? (
                <span className="launcher-badge">y / n</span>
              ) : (
                <span className="launcher-age">{locationAge(row.createdAt)}</span>
              ))}
          </div>
        </Fragment>
      ))}
    </div>
  );
}
