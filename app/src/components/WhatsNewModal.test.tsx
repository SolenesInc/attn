import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { WhatsNewModal } from './WhatsNewModal';

function renderModal(overrides: Partial<Parameters<typeof WhatsNewModal>[0]> = {}) {
  const props = {
    isOpen: true,
    onClose: vi.fn(),
    onViewShortcuts: vi.fn(),
    ...overrides,
  };
  render(<WhatsNewModal {...props} />);
  return props;
}

describe('WhatsNewModal', () => {
  it('renders nothing when closed', () => {
    const { container } = render(
      <WhatsNewModal isOpen={false} onClose={() => {}} onViewShortcuts={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });

  it('leads with desktops as the flagged callout', () => {
    renderModal();
    expect(screen.getByRole('dialog', { name: /desktops/i })).toBeInTheDocument();
    expect(screen.getByText('See every desktop at once')).toBeInTheDocument();
    expect(screen.getByText('Profiles group everything')).toBeInTheDocument();

    const hero = screen.getByText('Agents live on desktops').closest('.whats-new-item');
    expect(hero).not.toBeNull();
    expect(hero!.classList.contains('whats-new-item--key')).toBe(true);
    expect(hero!.querySelector('.whats-new-tag')?.textContent).toBe('Changed');
    expect(hero!.textContent).toContain('1–9');
  });

  it('dismisses and hands off to the full shortcuts list', () => {
    const onClose = vi.fn();
    const onViewShortcuts = vi.fn();
    renderModal({ onClose, onViewShortcuts });

    fireEvent.click(screen.getByRole('button', { name: 'View all shortcuts →' }));
    expect(onViewShortcuts).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Got it' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
