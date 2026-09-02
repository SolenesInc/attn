import { fireEvent, render, screen } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import {
  SeedDocumentView,
  type SeedDocument,
} from './SeedDocumentView';
import type { SeedDocumentNote } from './seedArtifacts';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'The plan',
    body: '## Rendered plan\n\nRead **this**.',
    status: 'growing',
    state_changed_at: '2026-08-15T08:00:00Z',
    state_changed_at_exact: true,
    step_slug: 'the-plan',
    planter_session: '',
    planter_member: '',
    tender_session: 'sess-a',
    tender_member: 'trellis',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    ready: false,
    rev: 1,
    created_at: '2026-08-15T08:00:00Z',
    updated_at: '2026-08-15T08:00:00Z',
    ...overrides,
  };
}

function note(overrides: Partial<SeedDocumentNote> & { id: string }): SeedDocumentNote {
  return {
    seed_id: 's-plan11',
    kind: 'note',
    body: '',
    author_session: '',
    author_member: '',
    created_at: '2026-08-15T09:00:00Z',
    ...overrides,
  };
}

function document(overrides: Partial<SeedDocument> = {}): SeedDocument {
  return {
    seed: seed(),
    tender_holds: false,
    children: [],
    notes: [],
    notes_total: 0,
    artifacts: [],
    references: [],
    ...overrides,
  };
}

describe('SeedDocumentView', () => {
  it('puts navigable plot work after seed status and before the annotatable body', () => {
    const child = seed({ id: 's-step11', title: 'Build the reader', body: '', status: 'harvested' });
    const onOpenSeed = vi.fn();
    render(
      <SeedDocumentView
        document={document({
          seed: seed({
            plot_progress: { total: 1, done: 1, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0 },
          }),
          children: [child],
          notes: [note({ id: 'n-one111', body: 'Verified the **reader**.', author_member: 'alder' })],
          notes_total: 2,
        })}
        onOpenSeed={onOpenSeed}
      />,
    );

    const details = screen.getByLabelText('Seed details');
    const plot = screen.getByRole('heading', { name: 'Plot' });
    const body = screen.getByRole('heading', { name: 'Rendered plan' });
    expect(details.compareDocumentPosition(plot) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(plot.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Build the reader/ }));
    expect(onOpenSeed).toHaveBeenCalledWith('s-step11');
    expect(screen.getByText('done')).toBeInTheDocument();

    const log = screen.getByText('Log').closest('details');
    expect(log?.open).toBe(false);
    fireEvent.click(screen.getByText('Log').closest('summary') as HTMLElement);
    expect(log?.open).toBe(true);
    expect(screen.getByText('reader', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('1 more entry on the log.')).toBeInTheDocument();
  });

  it('is read-only unless its tile owner explicitly enables annotations', () => {
    const { container } = render(<SeedDocumentView document={document()} />);

    expect(container.querySelector('.md-reader--annotating')).not.toBeInTheDocument();
  });

  it('renders the daemon’s artifact set and opens a current markdown artifact', () => {
    const current = { kind: 'markdown_file' as const, path: '/repo/current.md' };
    const notes = [
      note({ id: 'n-detach', kind: 'detach', artifact: { kind: 'markdown_file', path: '/repo/old.md' } }),
      note({ id: 'n-current', kind: 'attach', artifact: current }),
    ];

    const onOpenMarkdownArtifact = vi.fn();
    render(
      <SeedDocumentView
        document={document({ notes, notes_total: notes.length, references: [current] })}
        onOpenMarkdownArtifact={onOpenMarkdownArtifact}
      />,
    );

    expect(screen.queryByRole('button', { name: /old\.md/ })).not.toBeInTheDocument();
    const artifact = screen.getByRole('button', { name: /current\.md/ });
    expect(artifact.closest('li')).toHaveTextContent('linked file');
    expect(artifact.closest('li')).toHaveAttribute('title', '/repo/current.md');
    fireEvent.click(artifact);
    expect(onOpenMarkdownArtifact).toHaveBeenCalledWith('/repo/current.md');
  });

  it('renders a notebook artifact and a url artifact from the same set', () => {
    render(
      <SeedDocumentView
        document={document({
          references: [
            { kind: 'notebook', notebook_document_id: 'nb-plan-7' },
            { kind: 'url', url: 'https://example.test/pr/1' },
          ],
        })}
      />,
    );

    expect(screen.getByText('nb-plan-7')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /example\.test/ });
    expect(link).toHaveAttribute('href', 'https://example.test/pr/1');
    expect(link.closest('li')).toHaveTextContent('link');
  });

  it('says the harvest condition beside the state and opens the pull request', () => {
    render(
      <SeedDocumentView
        document={document({
          seed: seed({
            status: 'dormant',
            tender_session: '',
            tender_member: '',
            harvest_when: {
              pull_request: 'github.com:victorarias/attn#42',
              url: 'https://github.com/victorarias/attn/pull/42',
              set_at: '2026-09-02T10:00:00Z',
            },
          }),
        })}
      />,
    );

    const details = screen.getByLabelText('Seed details');
    const link = screen.getByRole('link', { name: /harvests when victorarias\/attn#42 merges/ });
    expect(details).toContainElement(link);
    expect(link).toHaveAttribute('href', 'https://github.com/victorarias/attn/pull/42');

    fireEvent.click(link);
    expect(openUrl).toHaveBeenCalledWith('https://github.com/victorarias/attn/pull/42');
  });

  it('says nothing about a harvest condition on a seed nobody armed', () => {
    render(<SeedDocumentView document={document()} />);

    expect(screen.queryByText(/harvests when/)).not.toBeInTheDocument();
  });

  it('renders no artifact section for an empty set', () => {
    render(
      <SeedDocumentView
        document={document({
          notes: [note({ id: 'n-plain1', body: 'No attachment here.' })],
          notes_total: 1,
        })}
      />,
    );

    expect(screen.queryByRole('heading', { name: 'Artifacts' })).not.toBeInTheDocument();
  });
});
