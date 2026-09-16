import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ActionMenu, type ActionMenuItem } from './ActionMenu';

function actions(overrides: Partial<ActionMenuItem>[] = []): ActionMenuItem[] {
  const base: ActionMenuItem[] = [
    {
      id: 'notebook-tile',
      title: 'Open Editor tile',
      description: 'Dock an editor beside your terminals',
      keywords: ['notebook'],
      icon: <span>C</span>,
      run: vi.fn(),
    },
    {
      id: 'attention',
      title: 'Open attention drawer',
      description: 'Show waiting work',
      keywords: ['notifications'],
      icon: <span>A</span>,
      run: vi.fn(),
    },
  ];
  return base.map((action, index) => ({ ...action, ...overrides[index] }));
}

describe('ActionMenu', () => {
  it('filters actions by keywords and runs the selected result', () => {
    const items = actions();
    const onClose = vi.fn();
    render(<ActionMenu isOpen actions={items} onClose={onClose} />);

    fireEvent.change(screen.getByLabelText('Search actions'), { target: { value: 'notebook' } });
    expect(screen.getByText('Open Editor tile')).toBeVisible();
    expect(screen.queryByText('Open attention drawer')).toBeNull();

    fireEvent.keyDown(screen.getByLabelText('Search actions'), { key: 'Enter' });
    expect(items[0].run).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('moves selection with arrow keys', () => {
    const items = actions();
    render(<ActionMenu isOpen actions={items} onClose={() => {}} />);

    const input = screen.getByLabelText('Search actions');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(items[1].run).toHaveBeenCalledOnce();
  });

  it('keeps focus handed to an action and returns it when dismissed', async () => {
    function Harness() {
      const [isOpen, setIsOpen] = useState(false);
      const [showTarget, setShowTarget] = useState(false);
      const items = actions([{ run: () => setShowTarget(true) }]);
      return (
        <>
          <button type="button" onClick={() => setIsOpen(true)}>Open menu</button>
          {showTarget && <input aria-label="Action target" autoFocus />}
          <ActionMenu isOpen={isOpen} actions={items} onClose={() => setIsOpen(false)} />
        </>
      );
    }

    render(<Harness />);
    const opener = screen.getByRole('button', { name: 'Open menu' });
    opener.focus();
    fireEvent.click(opener);
    fireEvent.keyDown(screen.getByLabelText('Search actions'), { key: 'Enter' });
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Action target' })).toHaveFocus());

    opener.focus();
    fireEvent.click(opener);
    const search = screen.getByLabelText('Search actions');
    await waitFor(() => expect(search).toHaveFocus());
    fireEvent.keyDown(search, { key: 'Escape' });
    await waitFor(() => expect(opener).toHaveFocus());
  });
});
