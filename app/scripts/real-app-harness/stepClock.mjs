import net from 'node:net';
import { once } from 'node:events';
import { createInterface } from 'node:readline';

export async function stepClock(socketPath, timeoutMs) {
  let socket;
  let connected;
  let connectionError;
  let pid;
  const connection = new Promise(resolve => { connected = resolve; });
  const server = net.createServer(peer => {
    socket = peer;
    const input = createInterface({ input: peer });
    const lines = input[Symbol.asyncIterator]();
    peer.on('error', error => {
      connectionError = error;
      input.close();
    });
    connected(lines);
  });
  server.listen(socketPath);
  await once(server, 'listening');

  async function command(name, duration) {
    const seen = [];
    let guard;
    const timeout = new Promise((_, reject) => {
      guard = setTimeout(() => reject(new Error(
        `step clock ${name} ${duration}: daemon ${pid ?? 'not connected'} did not acknowledge within ${timeoutMs}ms; saw timers [${seen.join(', ')}]`,
      )), timeoutMs);
    });
    const exchange = async () => {
      const lines = await connection;
      if (pid === undefined) {
        const greeting = await lines.next();
        const match = /^ready (\d+)$/.exec(greeting.value);
        if (!match) throw new Error(`step clock invalid greeting ${JSON.stringify(greeting.value)}: ${connectionError ?? 'connection closed'}`);
        pid = Number(match[1]);
      }
      socket.write(`${name} ${duration}\n`);
      for (;;) {
        const reply = await lines.next();
        if (reply.done) throw new Error(`step clock daemon ${pid} disconnected: ${connectionError ?? 'connection closed'}`);
        if (reply.value === 'ok') return;
        if (name === 'await' && reply.value.startsWith('seen ')) {
          seen.push(reply.value.slice('seen '.length));
          continue;
        }
        throw new Error(`step clock ${name} ${duration}: daemon ${pid} answered ${JSON.stringify(reply.value)}`);
      }
    };
    try {
      await Promise.race([exchange(), timeout]);
    } finally {
      clearTimeout(guard);
    }
  }

  return {
    env: { ATTN_STEP_CLOCK_SOCK: socketPath },
    advance: duration => command('advance', duration),
    awaitTimer: duration => command('await', duration),
    async close() {
      socket?.destroy();
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    },
  };
}
