import net from 'node:net';
import { once } from 'node:events';
import { createInterface } from 'node:readline';

const units = { h: 3_600_000, m: 60_000, s: 1_000, ms: 1, us: 0.001, 'µs': 0.001, ns: 0.000001 };

function durationMs(duration) {
  const parts = [...duration.matchAll(/(\d+(?:\.\d+)?)(h|ms|m|s|us|µs|ns)/g)];
  if (parts.length === 0 || parts.map(part => part[0]).join('') !== duration) {
    throw new Error(`step clock duration ${JSON.stringify(duration)} is not a Go duration`);
  }
  return parts.reduce((total, [, value, unit]) => total + Number(value) * units[unit], 0);
}

// stepClock steps every process started with its env as one clock, like internal/stepclock's Hub.
export async function stepClock(socketPath, timeoutMs) {
  const peers = new Set();
  const pending = new Map();
  let revision = 0;
  let marked = 0;
  let armedWaiters = [];

  const notifyArmed = () => {
    const waiters = armedWaiters;
    armedWaiters = [];
    for (const wake of waiters) wake();
  };

  const server = net.createServer(socket => {
    const peer = { socket, pid: undefined, advanced: [], closed: false, error: undefined };
    peers.add(peer);
    const input = createInterface({ input: socket });
    const finish = error => {
      if (peer.closed) return;
      peer.closed = true;
      peer.error = error;
      peers.delete(peer);
      for (const key of pending.keys()) {
        if (key.startsWith(`${peer.pid} `)) pending.delete(key);
      }
      for (const settle of peer.advanced.splice(0)) settle();
    };
    socket.on('error', finish);
    input.on('close', () => finish());
    input.on('line', line => {
      const fields = line.trim().split(/\s+/);
      if (peer.pid === undefined) {
        if (fields[0] !== 'ready' || !/^\d+$/.test(fields[1] ?? '')) {
          finish(new Error(`step clock invalid greeting ${JSON.stringify(line)}`));
          socket.destroy();
          return;
        }
        peer.pid = Number(fields[1]);
        return;
      }
      if (fields[0] === 'armed' && fields.length === 3) {
        revision += 1;
        pending.set(`${peer.pid} ${fields[1]}`, { ms: durationMs(fields[2]), revision });
        notifyArmed();
      } else if (fields[0] === 'disarmed' && fields.length === 2) {
        pending.delete(`${peer.pid} ${fields[1]}`);
      } else if (fields[0] === 'advanced' && fields.length === 2) {
        peer.advanced.shift()?.();
      } else if ((fields[0] === 'busy' || fields[0] === 'idle') && fields.length === 1) {
      } else {
        finish(new Error(`step clock process ${peer.pid} sent ${JSON.stringify(line)}`));
        socket.destroy();
      }
    });
  });
  server.listen(socketPath);
  await once(server, 'listening');

  async function guarded(describe, work) {
    let guard;
    const timeout = new Promise((_, reject) => {
      guard = setTimeout(() => reject(new Error(`${describe()} within ${timeoutMs}ms`)), timeoutMs);
    });
    try {
      return await Promise.race([work(), timeout]);
    } finally {
      clearTimeout(guard);
    }
  }

  function pendingDurations() {
    return [...pending.values()].map(timer => `${timer.ms}ms`).join(', ');
  }

  return {
    env: { ATTN_STEP_CLOCK_SOCK: socketPath },
    advance(duration) {
      durationMs(duration);
      marked = revision;
      const stepping = [...peers].filter(peer => peer.pid !== undefined);
      return guarded(() => `step clock advance ${duration}: processes [${stepping.filter(peer => !peer.closed).map(peer => peer.pid).join(', ')}] did not all advance`, () => Promise.all(stepping.map(peer => new Promise(resolve => {
        if (peer.closed) return resolve();
        peer.advanced.push(resolve);
        peer.socket.write(`advance ${duration}\n`);
      }))));
    },
    awaitTimer(duration) {
      const ms = durationMs(duration);
      return guarded(() => `step clock awaitTimer ${duration}: no such timer armed (pending: ${pendingDurations()})`, async () => {
        for (;;) {
          if ([...pending.values()].some(timer => timer.ms === ms && timer.revision > marked)) {
            marked = revision;
            return;
          }
          await new Promise(resolve => armedWaiters.push(resolve));
        }
      });
    },
    async close() {
      for (const peer of peers) peer.socket.destroy();
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    },
  };
}
