import { fireEvent, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { emptySelection, openDelegationSettings, type DelegationTable, type Role } from '../test/delegationDaemon';
import { gesture } from '../test/settings';

const build: Role = { id: 'build', name: 'Build', icon: 'code', enabled: true, description: 'Implement a change', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
const builder: Role = { ...build, id: 'builder', builtin: 'builder', name: 'Builder' };

async function openFallback(table: DelegationTable = {}) {
  const opened = await openDelegationSettings(table);
  fireEvent.click(screen.getByRole('button', { name: 'Anything else' }));
  const field = () => screen.getByLabelText('Instructions (optional)') as HTMLTextAreaElement;
  const write = (text: string) => {
    fireEvent.focus(field());
    fireEvent.change(field(), { target: { value: text } });
    fireEvent.blur(field());
  };
  const showSectionAgain = () => {
    fireEvent.click(screen.getByTestId('settings-nav-connectivity'));
    fireEvent.click(screen.getByTestId('settings-nav-delegation'));
    fireEvent.click(screen.getByRole('button', { name: 'Anything else' }));
  };
  const sentInstructions = () => opened.saves().map((save) => [save.preferences.revision, save.preferences.fallback.instructions]);
  return { ...opened, field, write, showSectionAgain, sentInstructions };
}

it('collapses edits made during a save into one follow-up carrying the returned revision', async () => {
  const { daemon, server, loads, field, write, sentInstructions } = await openFallback();
  server.holdSaves();
  await gesture(daemon, () => write('one'));
  await gesture(daemon, () => write('two'));
  await gesture(daemon, () => write('three'));
  expect(field().value).toBe('three');
  expect(sentInstructions()).toEqual([[0, 'one']]);

  await gesture(daemon, () => server.resumeSaves());
  expect(sentInstructions()).toEqual([[0, 'one'], [1, 'three']]);
  expect(server.preferences.fallback.instructions).toBe('three');
  expect(loads()).toHaveLength(1);

  await gesture(daemon, () => write('four'));
  expect(sentInstructions()[2]).toEqual([2, 'four']);
});

it('keeps an install queued behind a running save through the edits that collapse into it', async () => {
  const { daemon, server, saves, write } = await openFallback({ templates: [builder] });
  server.holdSaves();
  await gesture(daemon, () => write('one'));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Add Attn roles' })));
  await gesture(daemon, () => write('three'));
  await gesture(daemon, () => server.resumeSaves());
  expect(saves().map((save) => [save.preferences.fallback.instructions, save.install_workflow_skill])).toEqual([
    ['one', undefined],
    ['three', true],
  ]);
  expect(server.preferences.roles.map((role) => role.builtin)).toEqual(['builder']);
});

it('keeps undo when the push announcing its own save reloads the same revision', async () => {
  const { daemon, server, loads } = await openDelegationSettings({ roles: [build] });
  fireEvent.click(screen.getByRole('button', { name: 'Build' }));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
  expect(server.preferences.revision).toBe(1);

  await gesture(daemon, () => server.announce());
  expect(loads()).toHaveLength(2);
  expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();
});

it('loads again after a save when a newer revision was announced during the flight', async () => {
  const { daemon, server, loads, write } = await openFallback();
  server.holdSaves();
  await gesture(daemon, () => write('one'));
  await gesture(daemon, () => server.announce(2));
  expect(loads()).toHaveLength(1);

  await gesture(daemon, () => server.resumeSaves());
  expect(loads()).toHaveLength(2);
});

it('discards an edit made while the conflict reload is still loading', async () => {
  const { daemon, server, saves, field, write } = await openFallback();
  server.changeElsewhere();
  server.holdSaves();
  server.holdLoads();
  await gesture(daemon, () => write('one'));
  await gesture(daemon, () => server.answerHeldSave());
  expect(screen.getByRole('alert')).toHaveTextContent('reload before saving');

  await gesture(daemon, () => write('two'));
  await gesture(daemon, () => server.resumeLoads());
  expect(saves()).toHaveLength(1);
  expect(server.preferences.fallback.instructions).toBe('');
  expect(field().value).toBe('');
  expect(screen.getByRole('alert')).toHaveTextContent('reload before saving');
});

it('drops local edits and reloads when the daemon reports a conflict', async () => {
  const { daemon, server, loads, field, write, sentInstructions } = await openFallback();
  server.changeElsewhere((preferences) => { preferences.fallback.instructions = 'theirs'; });
  await gesture(daemon, () => write('mine'));
  expect(screen.getByRole('alert')).toHaveTextContent('reload before saving');
  expect(field().value).toBe('theirs');
  expect(loads()).toHaveLength(2);

  await gesture(daemon, () => write('mine again'));
  expect(sentInstructions()).toEqual([[0, 'mine'], [1, 'mine again']]);
});

it('reloads on a push while idle', async () => {
  const { daemon, server, saves, loads, field } = await openFallback();
  server.changeElsewhere((preferences) => { preferences.fallback.instructions = 'from elsewhere'; });
  await gesture(daemon, () => server.announce());
  expect(field().value).toBe('from elsewhere');
  expect(loads()).toHaveLength(2);
  expect(saves()).toHaveLength(0);
});

it('defers a reload asked for during a save until the save drains, so a queued edit is not rolled back or overwritten', async () => {
  const { daemon, server, loads, saves, field, write, showSectionAgain } = await openFallback();
  server.holdSaves();
  await gesture(daemon, () => write('one'));
  await gesture(daemon, () => showSectionAgain());
  await gesture(daemon, () => write('two'));
  expect(loads()).toHaveLength(1);

  await gesture(daemon, () => server.answerHeldSave());
  expect(saves()).toHaveLength(2);
  expect(loads()).toHaveLength(1);
  expect(field().value).toBe('two');

  await gesture(daemon, () => write('two!'));
  await gesture(daemon, () => server.resumeSaves());
  expect(server.preferences.fallback.instructions).toBe('two!');
  expect(field().value).toBe('two!');
  expect(loads()).toHaveLength(2);
});

it('shows the last confirmed table when a save fails and the recovery load fails too', async () => {
  const { daemon, server, loads, field, write, showSectionAgain } = await openFallback();
  server.changeElsewhere((preferences) => { preferences.fallback.instructions = 'theirs'; });
  server.failLoads('daemon unreachable');
  server.holdSaves();
  await gesture(daemon, () => write('mine'));
  expect(field().value).toBe('mine');

  await gesture(daemon, () => server.resumeSaves());
  expect(loads()).toHaveLength(2);
  expect(screen.getByRole('alert')).toHaveTextContent('daemon unreachable');
  expect(field().value).toBe('');

  server.failLoads('');
  await gesture(daemon, () => showSectionAgain());
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(field().value).toBe('theirs');
});
