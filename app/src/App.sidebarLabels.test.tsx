import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

const LONG = 'judge yielded stops so background waits stay green';
const LABEL_WIDTH = 180;

function measureLabels() {
  const measured = (axis: 'scrollWidth' | 'clientWidth', size: (label: HTMLElement) => number) => {
    const native = Object.getOwnPropertyDescriptor(Element.prototype, axis) ?? Object.getOwnPropertyDescriptor(HTMLElement.prototype, axis)!;
    Object.defineProperty(HTMLElement.prototype, axis, {
      configurable: true,
      get(this: HTMLElement) {
        return this.classList.contains('session-label') ? size(this) : native.get!.call(this);
      },
    });
    onTestFinished(() => {
      Object.defineProperty(HTMLElement.prototype, axis, native);
    });
  };
  measured('scrollWidth', (label) => (label.textContent?.length ?? 0) * 8);
  measured('clientWidth', () => LABEL_WIDTH);
}

async function renderSidebar() {
  measureLabels();
  await renderApp({
    initialState: {
      sessions: [daemonSession('long', { label: LONG }), daemonSession('short', { label: 'attn' })],
      workspaces: [agentWorkspace('long'), agentWorkspace('short')],
    },
  });
}

const row = (label: string) => screen.getByRole('button', { name: new RegExp(`^Open ${label}`) }).closest<HTMLElement>('.session-item')!;
const reveal = () => document.querySelector('[data-testid="session-label-reveal"]');

describe('App sidebar labels', () => {
  it('reveals the full name of a clipped session when its row is hovered, beside the label it leaves in place', async () => {
    await renderSidebar();
    expect(reveal()).toBeNull();

    fireEvent.pointerEnter(row(LONG));

    expect(reveal()).toHaveTextContent(LONG);
    expect(reveal()).toHaveAttribute('aria-hidden', 'true');
    expect(row(LONG)).toHaveTextContent(LONG);
  });

  it('reveals nothing for a name the row already shows in full', async () => {
    await renderSidebar();

    fireEvent.pointerEnter(row('attn'));

    expect(reveal()).toBeNull();
  });

  it.each([
    ['the pointer leaves', (target: HTMLElement) => fireEvent.pointerLeave(target)],
    ['the row is pressed, since a click can reorder the list under a still pointer', (target: HTMLElement) => fireEvent.pointerDown(target)],
    ['the sidebar scrolls, since the reveal is positioned once', (target: HTMLElement) => fireEvent.scroll(target)],
  ])('withdraws the reveal when %s', async (_, withdraw) => {
    await renderSidebar();
    fireEvent.pointerEnter(row(LONG));
    expect(reveal()).not.toBeNull();

    withdraw(row(LONG));

    expect(reveal()).toBeNull();
  });
});
