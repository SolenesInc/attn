import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { Seed } from '../types/generated';
import { SettingsProvider } from '../contexts/SettingsContext';
import { useGardenWalk } from '../store/gardenWalk';
import { GardenPanel } from './GardenPanel';
import type { SeedDocument } from './SeedDocumentView';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-call11',
    title: 'Choose the storage model',
    body: '## Background\n\nCompare both options.',
    status: 'growing',
    state_changed_at: '2026-09-07T08:00:00Z',
    state_changed_at_exact: true,
    step_slug: 'choose-storage-model',
    planter_session: '',
    planter_member: '',
    tender_session: 'session-agent',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    ready: false,
    rev: 2,
    created_at: '2026-09-07T08:00:00Z',
    updated_at: '2026-09-07T08:00:00Z',
    question: {
      id: 'q-call11',
      text: 'Should this be a document or its own table?',
      asked_at: '2026-09-07T08:30:00Z',
      asked_by_session: 'session-agent',
      asked_by_member: '',
      status: 'open',
    },
    ...overrides,
  };
}

function document(root: Seed): SeedDocument {
  return {
    seed: root,
    tender_holds: true,
    children: [],
    notes: [],
    notes_total: 0,
    artifacts: [],
    references: [],
  };
}

function renderPanel(enabled: boolean, overrides: Record<string, unknown> = {}) {
  const root = seed();
  return render(
    <SettingsProvider
      settings={{ garden_needs_human_enabled: enabled ? 'true' : 'false' }}
      setSetting={vi.fn()}
    >
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[root]}
        seedsTotal={1}
        fetchSeedDocument={vi.fn().mockResolvedValue(document(root))}
        onAnswerQuestion={vi.fn()}
        onDismissQuestion={vi.fn()}
        onClearQuestion={vi.fn()}
        {...overrides}
      />
    </SettingsProvider>,
  );
}

describe('GardenPanel pending decisions', () => {
  beforeEach(() => useGardenWalk.getState().setTrail([]));

  it('puts the flagged queue below search and marks its listing row', () => {
    renderPanel(true);

    const search = screen.getByRole('combobox', { name: 'Search the garden' });
    const band = screen.getByTestId('garden-needs-human');
    expect(search.compareDocumentPosition(band) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getAllByText('waiting on you').length).toBeGreaterThan(1);
    expect(screen.getByText('Should this be a document or its own table?')).toBeInTheDocument();
  });

  it('hides the queue and marks while the setting is off', () => {
    renderPanel(false);

    expect(screen.queryByTestId('garden-needs-human')).not.toBeInTheDocument();
    expect(screen.queryByText('waiting on you')).not.toBeInTheDocument();
  });

  it('shows the question above the seed body in the reader', async () => {
    renderPanel(true);
    fireEvent.click(screen.getByRole('button', { name: /Choose the storage model/ }));

    const question = await screen.findByLabelText('Question waiting on you');
    const body = await screen.findByRole('heading', { name: 'Background' });
    await waitFor(() => expect(question.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy());
  });
});
