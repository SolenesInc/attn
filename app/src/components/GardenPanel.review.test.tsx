import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { GardenPanel } from './GardenPanel';

describe('GardenPanel Review garden entry', () => {
  it('shows the candidate count and starts a new review', () => {
    const onOpenReview = vi.fn();
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[]}
        seedsTotal={0}
        reviewCandidateCount={3}
        onOpenReview={onOpenReview}
      />,
    );

    expect(screen.getByText('3 seeds need review')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Review garden' }));
    expect(onOpenReview).toHaveBeenCalledOnce();
  });

  it('names an unfinished run as a continuation', () => {
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[]}
        seedsTotal={0}
        reviewCandidateCount={1}
        reviewContinues
        onOpenReview={vi.fn()}
      />,
    );

    expect(screen.getByText('1 seed needs review')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Continue review' })).toBeInTheDocument();
  });
});
