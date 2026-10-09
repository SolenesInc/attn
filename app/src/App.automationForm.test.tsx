import { reportedModel } from './test/harnessCatalogs';
import { openRoute, pickModel, pickEffort, previewHarness, routeDialog, enterModel } from './test/harnessRoute';
import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import { HOLD, answerInTurn } from './test/scriptedDaemon';

type Definition = NonNullable<EventMessage<'automation_definitions_result'>['definitions']>[number];

const API_VERSION = 'attn.dev/automations/v1alpha1';

function definition(id: number, over: Partial<Definition> = {}): Definition {
  return {
    id,
    profile_id: 'default',
    name: 'PR reviewer',
    enabled: true,
    revision: 1,
    trigger_type: 'manual',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

const manualSpec = (name = 'PR reviewer', id: number | undefined = 1) => ({
  api_version: API_VERSION,
  id,
  name,
  trigger: { type: 'manual' },
  prompt: 'Do the work',
  launch: { driver: 'codex', model: 'gpt-5.6-luna', effort: 'medium' },
  location: { type: 'directory', path: '/tmp/work' },
});

const githubSpec = {
  api_version: API_VERSION,
  id: 1,
  name: 'Reviewer',
  trigger: { type: 'github_review_requested', repositories: { include: ['github.com/acme/widgets'] } },
  prompt: 'Review the PR',
  launch: { driver: 'claude', model: 'sonnet', effort: 'medium' },
  location: {
    type: 'repository_worktree',
    repository_sources: {
      default: { type: 'managed_cache' },
      overrides: { 'github.com/acme/widgets': { type: 'local_clone', path: '/home/user/widgets' } },
    },
  },
};

const sparseSpec = {
  api_version: API_VERSION,
  id: 1,
  name: 'Sparse launch',
  trigger: { type: 'manual' },
  prompt: 'Do the work',
  launch: { driver: 'codex' },
  location: { type: 'directory', path: '/tmp/work' },
};

interface Scene {
  definitions: Definition[];
  spec?: object;
}

const specResult = (scene: Scene): Reply => ({
  event: 'automation_definition_result',
  success: true,
  spec_json: JSON.stringify(scene.spec ?? manualSpec()),
  spec_yaml: '',
  definition: scene.definitions[0],
});

async function openAutomations(scene: Scene, script: (daemon: ScriptedDaemon) => void = () => {}) {
  const { daemon } = await renderApp();
  daemon.on('automation_definitions_get', () => ({
    event: 'automation_definitions_result',
    success: true,
    definitions: scene.definitions,
  }));
  daemon.on('automation_runs_get', ({ definition_id }) => ({
    event: 'automation_runs_result',
    success: true,
    definition_id,
    runs: [],
  }));
  daemon.on('automation_definition_get', () => specResult(scene));
  daemon.on('launch_desktop_get', () => ({
    event: 'launch_desktop_result',
    action: 'launch_desktop_get',
    success: true,
    items: [],
    desktops: [],
  }));
  daemon.on('automation_apply', ({ expected_id }) => ({
    event: 'automation_apply_result',
    success: true,
    definition: definition(expected_id || 4, { revision: 2 }),
    spec_yaml: '',
  }));
  daemon.on('automation_delete', () => ({ event: 'automation_delete_result', success: true }));
  daemon.on('automation_set_enabled', ({ definition_id, enabled }) => ({
    event: 'automation_set_enabled_result',
    success: true,
    definition: definition(definition_id, { enabled }),
  }));
  script(daemon);
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Automations' })));
  return daemon;
}

const field = (name: string) => screen.getByTestId(`automation-form-${name}`);
const type = (name: string, value: string) => fireEvent.change(field(name), { target: { value } });
const press = (daemon: ScriptedDaemon, name: string) => gesture(daemon, () => fireEvent.click(field(name)));

async function openNew(scene: Scene = { definitions: [definition(1)] }, script?: (daemon: ScriptedDaemon) => void) {
  const daemon = await openAutomations(scene, script);
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-new')));
  return daemon;
}

async function openEdit(scene: Scene, script?: (daemon: ScriptedDaemon) => void) {
  const daemon = await openAutomations(scene, script);
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-edit-1')));
  return daemon;
}

function fillManual() {
  type('name', 'My automation');
  type('directory-path', '/tmp/work');
  type('prompt', 'Do the work');
}

const applied = (daemon: ScriptedDaemon) =>
  daemon.sentOf('automation_apply').map(({ definition_yaml, expected_id, expected_revision }) => ({
    spec: JSON.parse(definition_yaml),
    expected_id,
    expected_revision,
  }));

describe('App automation form', () => {
  describe('opening and leaving', () => {
    it('opens an empty form for a new automation without reading any definition', async () => {
      const daemon = await openNew();

      expect(screen.getByTestId('automation-form')).toBeInTheDocument();
      expect(screen.queryByTestId('automations-panel-list')).toBeNull();
      expect(daemon.sentOf('automation_definition_get')).toEqual([]);
      expect(screen.queryByTestId('automation-form-enabled')).toBeNull();
    });

    it('opens the same form from the empty list', async () => {
      const daemon = await openAutomations({ definitions: [] });

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-new-empty')));

      expect(screen.getByTestId('automation-form')).toBeInTheDocument();
    });

    it('loads the definition it edits', async () => {
      const daemon = await openEdit({ definitions: [definition(1)] });

      expect(daemon.sentOf('automation_definition_get').map(({ definition_id }) => definition_id)).toEqual([1]);
      expect(field('name')).toHaveValue('PR reviewer');
    });

    it('goes back to the list on Cancel without saving', async () => {
      const daemon = await openNew();
      type('name', 'Unsaved');

      await press(daemon, 'cancel');

      expect(screen.queryByTestId('automation-form')).toBeNull();
      expect(screen.getByTestId('automations-panel-list')).toBeInTheDocument();
      expect(daemon.sentOf('automation_apply')).toEqual([]);
    });

    it('keeps the open form and what is typed in it when the daemon says automations changed', async () => {
      const daemon = await openEdit({ definitions: [definition(1)] });
      type('name', 'still typing');
      const listed = daemon.sentOf('automation_definitions_get').length;

      daemon.emit({ event: 'automations_changed', definition_ids: [1] });
      await daemon.idle();

      expect(daemon.sentOf('automation_definitions_get').length).toBeGreaterThan(listed);
      expect(daemon.sentOf('automation_definition_get')).toHaveLength(1);
      expect(field('name')).toHaveValue('still typing');
    });
  });

  describe('creating', () => {
    it('starts with the harness defaults and commits a discovered route only after a model choice', async () => {
      const daemon = await openNew();
      expect(field('route')).toHaveTextContent('Codex default');
      await openRoute(daemon, 'Automation model');
      expect(within(routeDialog().getByRole('listbox', { name: 'Harness' })).getAllByRole('option').map(option => option.textContent)).toEqual(['Claude', 'Codex']);
      await previewHarness(daemon, 'Claude');
      expect(field('route')).toHaveTextContent('Codex default');
      await pickModel(daemon, 'Opus 5.5');
      await pickEffort(daemon, 'high');
      expect(field('route')).toHaveTextContent('deep');
      expect(field('sentence')).toHaveTextContent('Opus 5.5');
      fillManual();
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual({ driver: 'claude', model: 'opus', effort: 'high' });
    });
    it('offers the profile\'s desktops before the new automation exists', async () => {
      const daemon = await openNew({ definitions: [] }, (scripted) => {
        scripted.on('launch_desktop_get', () => ({
          event: 'launch_desktop_result',
          action: 'launch_desktop_get',
          success: true,
          desktops: [
            { id: 'review', profile_id: 'profile-default', name: 'Review', order_key: 'a', tree_json: '', active_pane_id: '', panes: [], revision: 1 },
          ],
        }));
      });
      expect(daemon.sentOf('launch_desktop_get').some(({ item_id }) => item_id === '')).toBe(true);
      fireEvent.change(screen.getByLabelText('Desktop'), { target: { value: 'desktop:review' } });
      fillManual();
      await press(daemon, 'save');
      expect(daemon.sentOf('automation_apply')[0].launch_desktop_setting).toEqual({ desktop_id: 'review' });
    });

    it('lets the daemon assign the ID', async () => {
      await openNew();
      type('name', 'Nightly Sync');
      expect(screen.queryByTestId('automation-form-id')).toBeNull();
      expect(screen.queryByTestId('automation-form-id-customize')).toBeNull();
    });

    it('applies the spec it built as a new definition and returns to the list', async () => {
      const daemon = await openNew();
      fillManual();

      await press(daemon, 'save');

      expect(applied(daemon)).toEqual([
        { spec: { ...manualSpec('My automation'), id: undefined, launch: { driver: 'codex' } }, expected_id: 0, expected_revision: 0 },
      ]);
      expect(screen.queryByTestId('automation-form')).toBeNull();
      expect(screen.getByTestId('automations-panel-list')).toBeInTheDocument();
    });

    it('holds the form while the daemon applies it', async () => {
      const daemon = await openNew({ definitions: [definition(1)] }, (scripted) =>
        answerInTurn(scripted, 'automation_apply', [HOLD]),
      );
      fillManual();

      await press(daemon, 'save');

      expect(field('name')).toBeDisabled();
      expect(field('save')).toHaveTextContent('Saving…');
    });

    it('says a prompt is required once the user leaves the field empty, and stops saying so when one is typed', async () => {
      const daemon = await openNew();
      expect(screen.queryByTestId('automation-form-error-prompt')).toBeNull();

      await gesture(daemon, () => fireEvent.blur(field('prompt')));
      expect(field('error-prompt')).toHaveTextContent('A prompt is required.');

      await gesture(daemon, () => type('prompt', 'Review it'));
      expect(screen.queryByTestId('automation-form-error-prompt')).toBeNull();
    });

    it('refuses to save a schedule until the user chooses what happens to missed runs', async () => {
      const daemon = await openNew();
      fireEvent.click(field('trigger-scheduled'));
      expect(field('catchup-skip')).toBeInTheDocument();
      fillManual();
      type('cron', '0 9 * * *');

      await press(daemon, 'save');

      expect(field('error-catchUp')).toHaveTextContent('Choose what happens to missed runs.');
      expect(daemon.sentOf('automation_apply')).toEqual([]);
    });
  });

  describe('editing', () => {
    it('keeps unreported stored model and effort values visible and saves them unchanged', async () => {
      const spec = { ...manualSpec(), launch: { driver: 'codex', model: 'retired-model', effort: 'future-effort' } };
      const daemon = await openEdit({ definitions: [definition(1)], spec });
      expect(field('route')).toHaveTextContent('retired-model');
      expect(field('route')).toHaveTextContent('future-effort');
      await openRoute(daemon, 'Automation model');
      expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('future-effort');
      await gesture(daemon, () => fireEvent.mouseDown(document.body));
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual(spec.launch);
    });
    it('ignores a warmed global catalog when an executable override is selected', async () => {
      const spec = { ...manualSpec(), launch: { driver: 'codex', model: 'gpt-6-luna', effort: 'medium' } };
      const daemon = await openEdit({ definitions: [definition(1)], spec }, daemon => {
        daemon.on('harness_models', () => ({ event: 'harness_models_result', success: true, models: [reportedModel('codex', 'gpt-6-luna', 'Global Luna', 'light')], tier_defaults: { light: 'gpt-6-luna' }, detail: 'Global executable' }));
      });
      expect(field('route')).toHaveTextContent('Global Luna');
      const reads = daemon.sentOf('harness_models').length;
      await gesture(daemon, () => type('executable', '/opt/alternate-codex'));
      expect(field('route')).not.toHaveTextContent('Global Luna');
      expect(field('sentence')).not.toHaveTextContent('Global Luna');
      await openRoute(daemon, 'Automation model');
      expect(routeDialog().queryByRole('option', { name: /^Global Luna/ })).toBeNull();
      expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('medium');
      await gesture(daemon, () => fireEvent.mouseDown(field('name')));
      await gesture(daemon, () => type('executable', ''));
      expect(field('route')).toHaveTextContent('Global Luna');
      await gesture(daemon, () => type('executable', '/opt/alternate-codex'));
      await openRoute(daemon, 'Automation model');
      await enterModel(daemon, 'override-model');
      const effort = routeDialog().getByRole('textbox', { name: 'Effort' });
      fireEvent.change(effort, { target: { value: 'override-effort' } });
      await gesture(daemon, () => fireEvent.blur(effort));
      await gesture(daemon, () => fireEvent.mouseDown(field('name')));
      expect(daemon.sentOf('harness_models')).toHaveLength(reads);
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual({ driver: 'codex', model: 'override-model', effort: 'override-effort', executable: '/opt/alternate-codex' });
    });
    it('does not request global models for an existing executable override', async () => {
      const spec = { ...manualSpec(), launch: { driver: 'codex', model: 'override-model', effort: 'override-effort', executable: '/opt/alternate-codex' } };
      const daemon = await openEdit({ definitions: [definition(1)], spec });
      await openRoute(daemon, 'Automation model');
      expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('override-effort');
      expect(daemon.sentOf('harness_models')).toEqual([]);
      await gesture(daemon, () => fireEvent.mouseDown(field('name')));
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual(spec.launch);
    });
    it('offers manual model editing after a harness-list error', async () => {
      const daemon = await openEdit({ definitions: [definition(1)], spec: sparseSpec }, daemon => {
        daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Harness list unavailable', preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: [], templates: [] }));
      });
      await openRoute(daemon, 'Automation model');
      await enterModel(daemon, 'custom-model');
      await gesture(daemon, () => fireEvent.mouseDown(document.body));
      expect(field('route')).toHaveTextContent('custom-model');
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual({ driver: 'codex', model: 'custom-model' });
    });
    it('loads a GitHub definition into its fields and saves it against the revision it loaded', async () => {
      const daemon = await openEdit({
        definitions: [definition(1, { revision: 7, trigger_type: 'github_review_requested' })],
        spec: githubSpec,
      });

      expect(field('repositories-include-chip-0')).toHaveTextContent('github.com/acme/widgets');
      expect(field('route')).toHaveTextContent('Sonnet 5.5');
      expect(screen.getByText('Existing requests are left alone when enabled')).toBeInTheDocument();
      expect(field('sentence')).toHaveTextContent('fresh worktree at the PR head');
      expect(field('sentence')).toHaveTextContent('Sonnet 5.5');

      await press(daemon, 'save');

      expect(applied(daemon)).toEqual([{ spec: githubSpec, expected_id: 1, expected_revision: 7 }]);
    });

    it('rewrites the sentence when the trigger changes', async () => {
      await openEdit({ definitions: [definition(1)], spec: githubSpec });

      fireEvent.click(field('trigger-manual'));

      expect(field('sentence')).toHaveTextContent('Run now');
    });

    it('names a stored effort even when the harness chooses the model', async () => {
      const daemon = await openEdit({ definitions: [definition(1)], spec: { ...sparseSpec, launch: { driver: 'codex', effort: 'future-effort' } } });
      expect(field('sentence')).toHaveTextContent('Codex (future-effort effort)');
      await press(daemon, 'save');
      expect(applied(daemon)[0].spec.launch).toEqual({ driver: 'codex', effort: 'future-effort' });
    });
    it('keeps a launch that names only its agent that way, showing the agent defaults', async () => {
      const daemon = await openEdit({ definitions: [definition(1, { revision: 2 })], spec: sparseSpec });

      expect(field('route')).toHaveTextContent('Codex default');
      expect(field('sentence')).not.toHaveTextContent('effort');

      await press(daemon, 'save');

      expect(applied(daemon)[0].spec.launch).toEqual({ driver: 'codex' });
    });

    it('names a picked model in the sentence without an effort the agent chooses', async () => {
      const daemon = await openEdit({ definitions: [definition(1)], spec: sparseSpec });
      await openRoute(daemon, 'Automation model');
      await pickModel(daemon, 'gpt-5.6-luna');

      expect(field('sentence')).toHaveTextContent('(gpt-5.6-luna)');
      expect(field('sentence')).not.toHaveTextContent('effort');
    });

    it('saves a new named desktop beside the definition without putting it in the spec', async () => {
      const daemon = await openEdit({ definitions: [definition(1, { profile_id: 'work' })] });
      fireEvent.change(screen.getByLabelText('Desktop'), { target: { value: '__new' } });
      const dialog = screen.getByRole('dialog', { name: 'A new desktop' });
      fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Checks' } });
      await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Use this name' })));
      await press(daemon, 'save');
      const request = daemon.sentOf('automation_apply')[0];
      expect(request.launch_desktop_setting).toEqual({ desktop_name: 'Checks' });
      expect(request.profile_id).toBe('work');
      expect(JSON.parse(request.definition_yaml)).not.toHaveProperty('launch_desktop');
    });

    it('offers to reload when the definition changed elsewhere, and shows what is there now', async () => {
      const scene: Scene = { definitions: [definition(1, { revision: 3 })], spec: manualSpec('Original') };
      const daemon = await openEdit(scene, (scripted) => {
        scripted.on('automation_apply', () => ({
          event: 'automation_apply_result',
          success: false,
          error: 'automation definition changed elsewhere — reload before saving',
          error_code: 'revision_conflict',
        }));
      });
      expect(field('name')).toHaveValue('Original');

      await press(daemon, 'save');
      expect(field('stale-banner')).toBeInTheDocument();

      scene.definitions = [
        definition(1, {
          revision: 4,
          launch_desktop: { desktop_id: 'desktop-checks', label: 'New checks' },
        }),
      ];
      scene.spec = manualSpec('Changed elsewhere');
      await press(daemon, 'reload');

      expect(field('name')).toHaveValue('Changed elsewhere');
      expect(screen.getByLabelText('Desktop')).toHaveTextContent('New checks');
      expect(screen.queryByTestId('automation-form-stale-banner')).toBeNull();
      expect(daemon.sentOf('automation_definition_get')).toHaveLength(2);
    });

    it('turns the definition off and on from its toggle', async () => {
      const daemon = await openEdit({ definitions: [definition(1, { enabled: true })] });
      expect(field('enabled')).toHaveAttribute('aria-checked', 'true');

      await press(daemon, 'enabled');

      expect(
        daemon.sentOf('automation_set_enabled').map(({ definition_id, enabled }) => [definition_id, enabled]),
      ).toEqual([[1, false]]);
      expect(field('enabled')).toHaveAttribute('aria-checked', 'false');
    });

    it('deletes only on a second click, and forgets the first click when the user moves on', async () => {
      const daemon = await openEdit({ definitions: [definition(1)] });

      await press(daemon, 'delete');
      expect(screen.getByText('Confirm delete')).toBeInTheDocument();
      await gesture(daemon, () => fireEvent.mouseDown(field('name')));
      expect(screen.queryByText('Confirm delete')).toBeNull();
      expect(daemon.sentOf('automation_delete')).toEqual([]);

      await press(daemon, 'delete');
      await press(daemon, 'delete');

      expect(daemon.sentOf('automation_delete').map(({ definition_id }) => definition_id)).toEqual([1]);
      expect(screen.queryByTestId('automation-form')).toBeNull();
    });
  });
});
