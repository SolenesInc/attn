import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import { NeedsHumanBand, SeedQuestionCard } from './GardenQuestions';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-call11',
    title: 'Choose the storage model',
    body: 'Compare both options.',
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

describe('Garden questions', () => {
  it('answers the longest waiter from the panel band', async () => {
    const onAnswer = vi.fn().mockResolvedValue(undefined);
    render(
      <NeedsHumanBand
        seeds={[seed()]}
        onAnswer={onAnswer}
        onDismiss={vi.fn()}
        onClear={vi.fn()}
      />,
    );

    expect(screen.getByText('1 question needs you')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Answer it' }));
    fireEvent.change(screen.getByLabelText('Your answer'), { target: { value: 'Use a document for this.' } });
    fireEvent.click(screen.getByRole('button', { name: /^Answer/ }));

    await waitFor(() => expect(onAnswer).toHaveBeenCalledWith('s-call11', 'Use a document for this.'));
  });

  it('requires a reason before dismissing the ask', async () => {
    const onDismiss = vi.fn().mockResolvedValue(undefined);
    render(<SeedQuestionCard seed={seed()} onAnswer={vi.fn()} onDismiss={onDismiss} />);

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss…' }));
    expect(screen.getByRole('button', { name: /^Dismiss/ })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Reason for dismissing'), {
      target: { value: 'The agent already owns this implementation choice.' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Dismiss/ }));

    await waitFor(() => expect(onDismiss).toHaveBeenCalledWith(
      's-call11',
      'The agent already owns this implementation choice.',
    ));
  });

  it('keeps a withdrawn tombstone until it is cleared', async () => {
    const onClear = vi.fn().mockResolvedValue(undefined);
    render(
      <SeedQuestionCard
        seed={seed({ question: { ...seed().question!, status: 'withdrawn', resolved_at: '2026-09-07T09:00:00Z' } })}
        onClear={onClear}
      />,
    );

    expect(screen.getByText('Question withdrawn')).toBeInTheDocument();
    expect(screen.getByText('Should this be a document or its own table?')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(onClear).toHaveBeenCalledWith('s-call11'));
  });

  it('keeps the withdrawn question inspectable in the expanded queue', () => {
    const withdrawn = seed({
      question: { ...seed().question!, status: 'withdrawn', resolved_at: '2026-09-07T09:00:00Z' },
    });
    render(
      <NeedsHumanBand
        seeds={[withdrawn]}
        onAnswer={vi.fn()}
        onDismiss={vi.fn()}
        onClear={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Show' }));
    const row = document.querySelector('[data-seed-id="s-call11"]');
    expect(row).toHaveTextContent('Should this be a document or its own table?');
    expect(row).toContainElement(screen.getByTestId('garden-question-clear-s-call11'));
  });

  it('opens the next question when the selected queue row disappears', () => {
    const next = seed({
      id: 's-next11',
      title: 'Choose the cache policy',
      question: {
        ...seed().question!,
        id: 'q-next11',
        text: 'Should cache entries expire?',
        asked_at: '2026-09-07T08:45:00Z',
      },
    });
    const props = { onAnswer: vi.fn(), onDismiss: vi.fn(), onClear: vi.fn() };
    const { rerender } = render(<NeedsHumanBand seeds={[seed(), next]} {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Answer them' }));
    fireEvent.click(screen.getByTestId('garden-question-open-s-call11'));

    rerender(<NeedsHumanBand seeds={[next]} {...props} />);

    expect(screen.getByTestId('garden-question-open-s-next11')).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('garden-question-answer-input-s-next11')).toBeInTheDocument();
  });
});
