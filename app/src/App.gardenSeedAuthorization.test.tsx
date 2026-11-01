import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, sessionActor, sessionClaim, USER_ACTOR } from './test/daemonFixtures';
import { gardenRegion, openGarden, openRow, planted, type Garden } from './test/garden';
import { gesture } from './test/renderApp';

const BUILDER = { ref: 'session:builder-a', name: 'builder-a', session_id: 'builder-a' };

const tended = planted('s-auth11', 'Search endpoint', { status: 'growing', ...sessionClaim('builder-a') });

const authorizationSection = () => within(within(gardenRegion()).getByRole('heading', { name: 'Authorization' }).closest('section')!);

async function openSeed(seed = tended): Promise<Garden> {
  const garden = await openGarden([seed], { sessions: [daemonSession('s1'), daemonSession('builder-a')] });
  await openRow(garden.daemon, 'Search endpoint');
  return garden;
}

function answerAuthorization(garden: Garden, next: (text: string | undefined) => typeof tended) {
  garden.daemon.on('seed_authorization', ({ request_id, text }) => {
    const seed = next(text);
    garden.push([seed]);
    return { event: 'seed_authorization_result', request_id, success: true, result: { seed, changed: true, update: seed.authorization?.update } };
  });
}

describe('App garden seed authorization', () => {
  it('shows the words, who set them and that the update waits for a tender that needs approval', async () => {
    await openSeed({
      ...tended,
      authorization: {
        text: 'You may land on main.',
        set_by: sessionActor('orch-1', 'orchestrator-o'),
        set_at: '2026-11-01T10:42:00Z',
        update: { recipient: BUILDER, needs_approval: true },
      },
    });

    const section = authorizationSection();
    expect(section.getByText('You may land on main.')).toBeInTheDocument();
    expect(section.getByText('set by orchestrator-o')).toBeInTheDocument();
    expect(section.getByText('waiting for builder-a, which needs approval')).toBeInTheDocument();
  });

  it('sets the words from the keyboard as the user and shows them typed into the tender', async () => {
    const garden = await openSeed();
    answerAuthorization(garden, (text) => ({
      ...tended,
      authorization: { text: text!, set_by: USER_ACTOR, set_at: '2026-11-01T10:44:00Z', update: { recipient: BUILDER, typed_at: '2026-11-01T10:44:01Z' } },
    }));
    expect(authorizationSection().getByText('No authorization from the user for this task.')).toBeInTheDocument();

    await gesture(garden.daemon, () => fireEvent.click(authorizationSection().getByRole('button', { name: 'Set' })));
    const input = authorizationSection().getByRole('textbox', { name: /Authorize s-auth11/ });
    expect(input).toHaveFocus();
    fireEvent.change(input, { target: { value: '  Ask me before pushing.  ' } });
    await gesture(garden.daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(garden.daemon.sentOf('seed_authorization')).toEqual([
      expect.objectContaining({ seed_id: 's-auth11', text: 'Ask me before pushing.' }),
    ]);
    expect(garden.daemon.sentOf('seed_authorization')[0]).not.toHaveProperty('source_session_id');
    const section = authorizationSection();
    expect(section.getByText('Ask me before pushing.')).toBeInTheDocument();
    expect(section.getByText('set by you')).toBeInTheDocument();
    expect(section.getByText(/^typed into builder-a at /)).toBeInTheDocument();
    expect(section.getByRole('button', { name: 'Change' })).toHaveFocus();
  });

  it('clears the words and leaves the keyboard on Set', async () => {
    const garden = await openSeed({
      ...tended,
      authorization: { text: 'You may land on main.', set_by: USER_ACTOR, set_at: '2026-11-01T10:42:00Z' },
    });
    answerAuthorization(garden, () => ({ ...tended }));

    await gesture(garden.daemon, () => fireEvent.click(authorizationSection().getByRole('button', { name: 'Clear' })));

    expect(garden.daemon.sentOf('seed_authorization')).toEqual([expect.objectContaining({ seed_id: 's-auth11', clear: true })]);
    expect(authorizationSection().queryByText('You may land on main.')).not.toBeInTheDocument();
    expect(authorizationSection().getByText('No authorization from the user for this task.')).toBeInTheDocument();
    expect(authorizationSection().getByRole('button', { name: 'Set' })).toHaveFocus();
  });

  it('keeps the words in the field and shows the refusal when the daemon refuses', async () => {
    const garden = await openSeed();
    garden.daemon.on('seed_authorization', ({ request_id }) => ({
      event: 'seed_authorization_result', request_id, success: false, error: 'no seed s-auth11 is planted here',
    }));

    await gesture(garden.daemon, () => fireEvent.click(authorizationSection().getByRole('button', { name: 'Set' })));
    const input = authorizationSection().getByRole('textbox', { name: /Authorize s-auth11/ });
    fireEvent.change(input, { target: { value: 'You may release.' } });
    await gesture(garden.daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(screen.getByRole('alert')).toHaveTextContent('no seed s-auth11 is planted here');
    expect(input).toHaveValue('You may release.');
  });

  it('sends nothing when the edit is cancelled with Escape', async () => {
    const garden = await openSeed();

    await gesture(garden.daemon, () => fireEvent.click(authorizationSection().getByRole('button', { name: 'Set' })));
    const input = authorizationSection().getByRole('textbox', { name: /Authorize s-auth11/ });
    fireEvent.change(input, { target: { value: 'You may release.' } });
    await gesture(garden.daemon, () => fireEvent.keyDown(input, { key: 'Escape' }));

    expect(garden.daemon.sentOf('seed_authorization')).toEqual([]);
    expect(authorizationSection().queryByRole('textbox')).not.toBeInTheDocument();
    expect(authorizationSection().getByRole('button', { name: 'Set' })).toHaveFocus();
  });
});
