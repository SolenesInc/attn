import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';

const SPAWN_TIMEOUT_MS = 30_000;
const CONVERSATION_TIMEOUT_MS = 30_000;
const CLOSE_TIMEOUT_MS = 20_000;

function conversationRecorded(binary, env, sessionId) {
  try {
    execFileSync(binary, ['session', 'transcript', sessionId, '--json'], { env, stdio: 'ignore', timeout: 10_000 });
    return true;
  } catch {
    return false;
  }
}

export async function workInFolder({ observer, binary, env, cwd }) {
  const id = crypto.randomUUID();
  const spawned = observer.waitForMessage(
    (message) => message.event === 'spawn_result' && message.id === id && message,
    `agent session in ${cwd} to spawn`,
    SPAWN_TIMEOUT_MS,
  );
  observer.send({ cmd: 'spawn_session', id, agent: 'claude', cwd, profile_id: observer.profileId, placement: {}, label: 'Worked here', cols: 100, rows: 30 });
  const result = await spawned;
  if (!result.success) throw new Error(`agent session in ${cwd} did not spawn: ${result.error || 'no error given'}`);
  const startedAt = Date.now();
  while (!conversationRecorded(binary, env, id)) {
    if (Date.now() - startedAt > CONVERSATION_TIMEOUT_MS) {
      throw new Error(`agent session ${id} in ${cwd} recorded no conversation within ${CONVERSATION_TIMEOUT_MS}ms`);
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  observer.unregisterSession(id);
  await observer.waitFor(() => (observer.getSession(id) ? null : true), `agent session ${id} in ${cwd} to close`, CLOSE_TIMEOUT_MS);
  return id;
}
