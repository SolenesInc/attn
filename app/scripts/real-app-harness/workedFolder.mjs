import crypto from 'node:crypto';

const SPAWN_TIMEOUT_MS = 30_000;
const CONVERSATION_TIMEOUT_MS = 30_000;
const CLOSE_TIMEOUT_MS = 20_000;

function sessionsIn(message) {
  return message.session ? [message.session] : message.sessions || [];
}

export async function workInFolder({ observer, cwd }) {
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage(
    (message) => message.event === 'spawn_result' && message.id === id && message,
    `agent session in ${cwd} to spawn`,
    SPAWN_TIMEOUT_MS,
  );
  const recorded = observer.waitForMessage(
    (message) => sessionsIn(message).some((session) => session.id === id && session.conversation_started),
    `agent session ${id} in ${cwd} to start a conversation`,
    SPAWN_TIMEOUT_MS + CONVERSATION_TIMEOUT_MS,
  );
  recorded.catch(() => {});
  observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label: 'Worked here', cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error(`agent session in ${cwd} did not spawn: ${result.error || 'no error given'}`);
  await recorded;
  const closed = observer.waitForMessage(
    (message) => message.event === 'session_unregistered' && message.session?.id === id,
    `agent session ${id} in ${cwd} to close`,
    CLOSE_TIMEOUT_MS,
  );
  observer.unregisterSession(id);
  await closed;
  return id;
}
