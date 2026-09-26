import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { openSection } from './test/settings';

async function openNewSession(settings: Record<string, string>) {
  const { daemon } = await renderApp({ initialState: { settings } });
  await gesture(daemon, () => pressShortcut('session.new'));
  const options = within(screen.getByRole('radiogroup', { name: /agent/i })).getAllByRole('radio') as HTMLButtonElement[];
  const name = (option: HTMLElement) => option.querySelector('.agent-option-name')?.textContent;
  return {
    offered: options.filter((option) => !option.disabled).map(name),
    unavailable: options.filter((option) => option.disabled).map(name),
    chosen: options.filter((option) => option.getAttribute('aria-checked') === 'true').map(name),
  };
}

function settingsAgents() {
  return Array.from(document.querySelectorAll('.settings-agent'), (agent) => ({
    name: agent.querySelector('summary > span')?.textContent,
    capabilities: Array.from(agent.querySelectorAll('.settings-agent-capabilities > div'), (capability) => (
      `${capability.querySelector('dt')?.textContent}: ${capability.querySelector('dd')?.textContent}`
    )),
  }));
}

describe('App new session', () => {
  it.each([
    [
      'the built-in agents when the daemon says nothing',
      {},
      { offered: ['Terminal', 'Claude', 'Codex', 'Copilot'], unavailable: [], chosen: ['Claude'] },
    ],
    [
      'the agents the daemon advertises, plugins included',
      { claude_available: 'false', pi_available: 'true', 'gemini-cli_available': 'true' },
      { offered: ['Terminal', 'Codex', 'Copilot', 'Gemini Cli', 'Pi'], unavailable: ['Claude'], chosen: ['Codex'] },
    ],
    [
      'the first available agent when the preferred one is missing',
      { new_session_agent: 'claude', claude_available: 'false', codex_available: 'false' },
      { offered: ['Terminal', 'Copilot'], unavailable: ['Claude', 'Codex'], chosen: ['Copilot'] },
    ],
    [
      'a terminal when no agent CLI is available',
      { codex_available: 'false', claude_available: 'false', copilot_available: 'false' },
      { offered: ['Terminal'], unavailable: ['Claude', 'Codex', 'Copilot'], chosen: ['Terminal'] },
    ],
  ])('offers %s', async (_, settings, expected) => {
    expect(await openNewSession(settings)).toEqual(expected);
  });

  it('lists the preferred agent first in Settings, with what each agent supports', async () => {
    await openSection('agents', {
      settings: {
        new_session_agent: 'pi',
        pi_available: 'true',
        mistral_available: 'true',
        codex_cap_transcript_watcher: 'true',
        codex_cap_classifier: 'false',
        pi_cap_custom_cap: 'true',
      },
    });

    expect(settingsAgents()).toEqual([
      { name: 'Pi', capabilities: ['custom cap: Supported'] },
      { name: 'Codex', capabilities: ['Transcript watch: Supported', 'Classifier: Unavailable'] },
      { name: 'Claude', capabilities: [] },
      { name: 'Copilot', capabilities: [] },
      { name: 'Mistral', capabilities: [] },
    ]);
  });

  it('shows the executables attn launches agents with, and nothing else ending in _executable', async () => {
    await openSection('agents', { settings: { codex_executable: '/usr/local/bin/codex', snipe_executable: '/opt/snipe' } });

    expect(screen.getByLabelText('Executable', { selector: '#settings-codex-exec' })).toHaveValue('/usr/local/bin/codex');
    expect(settingsAgents().map(({ name }) => name)).toEqual(['Claude', 'Codex', 'Copilot']);
  });
});
