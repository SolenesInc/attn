import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { chosenRow, destinationMemory, HOME, launchedAt, openPicker, pathInput, press, repoInfo, submitPath } from './test/locations';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
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

const REPO = `${HOME}/projects/repo`;
const FEATURE = `${REPO}--feature`;
const MEMORY = destinationMemory(REPO);
const WITH_FEATURE = repoInfo(REPO, [{ path: FEATURE, branch: 'feature' }], {
  branches: [{ name: 'main' }, { name: 'feature' }, { name: 'spike' }],
});

async function openChooser(path = REPO, settings: Record<string, string> = {}) {
  const view = await openPicker({ repos: [WITH_FEATURE] }, { settings });
  await submitPath(view.daemon, path);
  return view;
}

const chooser = () => screen.getByTestId('repo-options');
const createInput = () => screen.getByTestId('repo-new-worktree-input') as HTMLInputElement;
const rows = () => screen.queryAllByTestId(/^repo-option-\d+$/);
const deletePrompt = () => screen.queryByText(/Delete repo--feature/);

async function inChooser(daemon: ScriptedDaemon, key: string) {
  await press(daemon, key, {}, chooser());
}

describe('App new session', () => {
  describe('choosing an agent', () => {
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

  describe('in a repository', () => {
    it('lands on the worktree a typed path names, listing the checkout and its worktrees but not its branches', async () => {
      await openChooser(FEATURE);

      expect(rows()).toHaveLength(2);
      expect(chosenRow()).toBe(1);
      expect(screen.getByTestId('repo-option-1')).toHaveTextContent('feature');
    });

    it('opens a typed existing worktree on Enter without creating one, even when the repository was last run in its checkout', async () => {
      const { daemon } = await openChooser(FEATURE, { [MEMORY]: 'main_repo' });
      expect(chosenRow()).toBe(1);

      await inChooser(daemon, 'Enter');

      expect(daemon.sentOf('create_worktree')).toEqual([]);
      expect(launchedAt(daemon)).toEqual([{ cwd: FEATURE, agent: 'claude' }]);
    });

    it('offers a generated adjective-noun worktree name for the repository root, and draws another on request', async () => {
      const { daemon } = await openChooser();
      expect(createInput()).toHaveFocus();
      expect(createInput().value).toMatch(/^[a-z]+-[a-z]+$/);
      expect(chosenRow()).toBe(-1);
      const offered = createInput().value;

      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Pick another name' })));

      expect(createInput().value).toMatch(/^[a-z]+-[a-z]+$/);
      expect(createInput().value).not.toBe(offered);
    });

    it('keeps the create form focused for a repository last used through a new worktree', async () => {
      await openChooser(REPO, { [MEMORY]: 'new_worktree' });

      expect(createInput()).toHaveFocus();
    });

    it('moves between the create form and the destinations with the arrows', async () => {
      const { daemon } = await openChooser(REPO, { [MEMORY]: 'main_repo' });
      expect(chosenRow()).toBe(0);

      await inChooser(daemon, 'ArrowUp');
      expect(createInput()).toHaveFocus();

      await inChooser(daemon, 'ArrowDown');
      expect(chosenRow()).toBe(0);
      await inChooser(daemon, 'Enter');
      expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'claude' }]);
    });

    it('creates a worktree from origin/<default branch>, or from the chosen worktree’s branch, and starts the session in it', async () => {
      const { daemon } = await openChooser(FEATURE);
      const create = async (name: string, from?: 'current') => {
        await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-new-worktree-form')));
        fireEvent.change(createInput(), { target: { value: name } });
        if (from) fireEvent.click(screen.getByTestId('repo-new-worktree-start-current'));
        else expect(screen.getByTestId('repo-new-worktree-start-default')).toBeChecked();
        await inChooser(daemon, 'Enter');
      };

      await create('feature-2');
      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await submitPath(daemon, FEATURE);
      await create('feature-3', 'current');

      expect(daemon.sentOf('create_worktree').map(({ branch, starting_from }) => ({ branch, starting_from }))).toEqual([
        { branch: 'feature-2', starting_from: 'origin/main' },
        { branch: 'feature-3', starting_from: 'feature' },
      ]);
      expect(launchedAt(daemon).map(({ cwd }) => cwd)).toEqual([`${REPO}--feature-2`, `${REPO}--feature-3`]);
    });

    it.each([
      ['a branch that already exists', "git worktree add failed: fatal: a branch named 'mine' already exists"],
      ['any other failure', 'git worktree add failed: fatal: not a git repository'],
    ])('reports %s for a typed worktree name once, without retrying or launching', async (_, error) => {
      const { daemon } = await openChooser();
      daemon.on('create_worktree', () => ({ event: 'create_worktree_result', success: false, error }));

      fireEvent.change(createInput(), { target: { value: 'mine' } });
      await inChooser(daemon, 'Enter');

      expect(daemon.sentOf('create_worktree').map(({ branch }) => branch)).toEqual(['mine']);
      expect(screen.getByRole('alert')).toHaveTextContent(error);
      expect(launchedAt(daemon)).toEqual([]);
    });

    it('leaves the letter d to the create form rather than arming a delete', async () => {
      const { daemon } = await openChooser(FEATURE);
      await inChooser(daemon, 'ArrowUp');
      await inChooser(daemon, 'ArrowUp');
      expect(createInput()).toHaveFocus();

      await inChooser(daemon, 'd');

      expect(deletePrompt()).toBeNull();
    });

    it('drops a delete prompt on Escape without leaving the chooser, and when the create form takes focus', async () => {
      const { daemon } = await openChooser(FEATURE);

      await inChooser(daemon, 'd');
      expect(deletePrompt()).not.toBeNull();
      await inChooser(daemon, 'Escape');
      expect(deletePrompt()).toBeNull();
      expect(chooser()).toBeInTheDocument();

      await inChooser(daemon, 'd');
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-new-worktree-form')));
      expect(deletePrompt()).toBeNull();
      expect(createInput()).toHaveFocus();
      expect(daemon.sentOf('delete_worktree')).toEqual([]);
    });

    it('goes back to the path from the create form on Escape', async () => {
      const { daemon } = await openChooser();

      await inChooser(daemon, 'Escape');

      expect(screen.queryByTestId('repo-options')).toBeNull();
      expect(pathInput()).toBeInTheDocument();
    });

    it('keeps the destinations listed while the repository is read again', async () => {
      const { daemon } = await openChooser(FEATURE);
      daemon.on('get_repo_info', () => undefined);

      await inChooser(daemon, 'r');

      expect(screen.getByRole('status', { name: 'Refreshing repo options' })).toBeInTheDocument();
      expect(rows()).toHaveLength(2);
    });

    it('shows which worktree is being deleted until the daemon answers', async () => {
      const { daemon } = await openChooser(FEATURE);
      daemon.on('delete_worktree', () => undefined);

      await inChooser(daemon, 'd');
      await inChooser(daemon, 'y');

      expect(screen.getByRole('status', { name: 'Deleting repo--feature' })).toBeInTheDocument();
      expect(deletePrompt()).toBeNull();
      expect(daemon.sentOf('delete_worktree')).toEqual([{ cmd: 'delete_worktree', path: FEATURE }]);
    });
  });
});
