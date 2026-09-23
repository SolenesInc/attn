import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MigrationFailureScreen } from './MigrationFailureScreen';
import { migrationFailureFromMarker } from '../utils/migrationFailure';

function renderedFacts(): Array<[string, string]> {
  return Array.from(document.querySelectorAll('.migration-failure-fact')).map((row) => [
    row.querySelector('dt')?.textContent ?? '',
    row.querySelector('dd')?.textContent ?? '',
  ]);
}

describe('MigrationFailureScreen', () => {
  it('shows every fact in the marker, known ones first, then the marker path', () => {
    const failure = migrationFailureFromMarker({
      marker_path: '/Users/u/.attn/migration-failure.json',
      contents: JSON.stringify({
        failed_at: '2026-09-23T10:00:00Z',
        worker_count: 3,
        error: 'desktop tree for workspace ws-4 is invalid',
        schema_version_to: 152,
        schema_version_from: 151,
        binary_version: '0.42.0',
        backup_path: '/Users/u/.attn/backups/attn-151.db',
        database_path: '/Users/u/.attn/attn.db',
        data_dir: '/Users/u/.attn',
        daemon_log_path: '/Users/u/.attn/daemon.log',
      }),
    });

    render(<MigrationFailureScreen failure={failure} />);

    expect(screen.getByRole('heading').textContent).toBe('attn seems broken after migration');
    expect(screen.getByText(/Ask an agent outside attn to investigate/)).toBeTruthy();
    expect(renderedFacts()).toEqual([
      ['Daemon log', '/Users/u/.attn/daemon.log'],
      ['Data directory', '/Users/u/.attn'],
      ['Database', '/Users/u/.attn/attn.db'],
      ['Backup', '/Users/u/.attn/backups/attn-151.db'],
      ['Schema version before', '151'],
      ['Schema version after', '152'],
      ['Error', 'desktop tree for workspace ws-4 is invalid'],
      ['attn version', '0.42.0'],
      ['Failed at', '2026-09-23T10:00:00Z'],
      ['worker_count', '3'],
      ['Failure marker', '/Users/u/.attn/migration-failure.json'],
    ]);
    expect(screen.queryByRole('button', { name: /retry/i })).toBeNull();
  });

  it('shows an unreadable marker verbatim rather than hiding it', () => {
    const failure = migrationFailureFromMarker({
      marker_path: '/tmp/attn/migration-failure.json',
      contents: 'conversion aborted before writing JSON',
    });

    render(<MigrationFailureScreen failure={failure} />);

    expect(renderedFacts()).toEqual([
      ['Marker contents (not a JSON object)', 'conversion aborted before writing JSON'],
      ['Failure marker', '/tmp/attn/migration-failure.json'],
    ]);
  });
});
