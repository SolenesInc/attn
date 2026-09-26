import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { openSection, savedSettings } from './test/settings';

const headless = {
  codex_cap_headless_task: 'true',
  claude_cap_headless_task: 'true',
  copilot_cap_headless_task: 'true',
};

function openAdvisor(settings: Record<string, string> = {}) {
  return openSection('backgroundAgents', { settings: { ...headless, ...settings } });
}

const advisor = () => within(screen.getByRole('heading', { name: 'Garden advisor' }).closest('section')!);
const field = (label: string) => advisor().getByLabelText(label);
const recipe = () => ({
  agent: (field('Agent') as HTMLSelectElement).value,
  model: (field('Model') as HTMLSelectElement).value,
  effort: (field('Reasoning effort') as HTMLSelectElement).value,
});

function savedRecipes(daemon: ScriptedDaemon): Array<{ agent: string; model: string; effort?: string }> {
  return savedSettings(daemon).filter(([key]) => key === 'garden.advisor').map(([, value]) => JSON.parse(value));
}

describe('App garden advisor settings', () => {
  it.each([
    ['no setting', {}],
    ['a setting it cannot read', { 'garden.advisor': 'not json' }],
  ])('shows the Codex recipe with %s', async (_, settings) => {
    await openAdvisor(settings);

    expect(recipe()).toEqual({ agent: 'codex', model: 'gpt-5.6-luna', effort: 'xhigh' });
  });

  it('fills in the chosen agent’s defaults and saves them as one recipe', async () => {
    const daemon = await openAdvisor();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'claude' } }));

    expect(recipe()).toEqual({ agent: 'claude', model: 'sonnet', effort: 'medium' });
    expect(savedSettings(daemon)).toEqual([['garden.advisor', '{"agent":"claude","model":"sonnet","effort":"medium"}']]);
  });

  it('leaves Copilot’s effort to Copilot until the user pins one', async () => {
    const daemon = await openAdvisor();

    await gesture(daemon, () => fireEvent.change(field('Agent'), { target: { value: 'copilot' } }));
    expect(recipe()).toEqual({ agent: 'copilot', model: 'claude-sonnet-4.6', effort: '' });

    expect(savedSettings(daemon)[0]).toEqual(['garden.advisor', '{"agent":"copilot","model":"claude-sonnet-4.6"}']);

    await gesture(daemon, () => fireEvent.change(field('Reasoning effort'), { target: { value: 'max' } }));

    expect(savedRecipes(daemon)[1]).toEqual({ agent: 'copilot', model: 'claude-sonnet-4.6', effort: 'max' });
  });

  it('saves a custom model once the user leaves the field', async () => {
    const daemon = await openAdvisor();
    fireEvent.change(field('Model'), { target: { value: 'custom' } });
    fireEvent.change(field('Custom model'), { target: { value: 'gpt-custom' } });

    await gesture(daemon, () => fireEvent.blur(field('Custom model')));

    expect(savedSettings(daemon)).toEqual([['garden.advisor', '{"agent":"codex","model":"gpt-custom","effort":"xhigh"}']]);
  });

  it('keeps a saved agent that can no longer run visible, with a warning', async () => {
    await openAdvisor({
      'garden.advisor': '{"agent":"copilot","model":"claude-sonnet-4.6"}',
      copilot_available: 'false',
    });

    expect(field('Agent')).toHaveValue('copilot');
    expect(advisor().getByText(/Copilot is saved for Garden review but cannot run headless tasks here/)).toBeInTheDocument();
  });
});
