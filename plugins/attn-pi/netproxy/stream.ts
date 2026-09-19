import { connect, type Socket } from "node:net";

export class ConnectionClosedError extends Error {
  constructor(message = "the proxy client closed the connection") {
    super(message);
    this.name = "ConnectionClosedError";
  }
}

export class SocketReader {
  private buffer: Buffer = Buffer.alloc(0);
  private ended = false;
  private failure: Error | undefined;
  private wake: (() => void) | undefined;
  private detached = false;

  // Pulls one chunk per demand: a held connection must not buffer unbounded bytes while waiting.
  private readonly onData = (chunk: Buffer): void => {
    this.socket.pause();
    this.buffer = Buffer.concat([this.buffer, chunk]);
    this.notify();
  };

  private readonly onEnd = (): void => {
    this.ended = true;
    this.notify();
  };

  private readonly onError = (error: Error): void => {
    this.failure = error;
    this.ended = true;
    this.notify();
  };

  constructor(private readonly socket: Socket) {
    socket.pause();
    socket.on("data", this.onData);
    socket.on("end", this.onEnd);
    socket.on("close", this.onEnd);
    socket.on("error", this.onError);
  }

  get buffered(): number {
    return this.buffer.length;
  }

  async need(count: number): Promise<void> {
    while (this.buffer.length < count) await this.readMore();
  }

  private async readMore(): Promise<void> {
    if (this.failure) throw this.failure;
    if (this.ended) throw new ConnectionClosedError();
    this.socket.resume();
    await new Promise<void>((resolve) => {
      this.wake = resolve;
    });
  }

  async peek(count: number): Promise<Buffer> {
    await this.need(count);
    return this.buffer.subarray(0, count);
  }

  async take(count: number): Promise<Buffer> {
    await this.need(count);
    const head = this.buffer.subarray(0, count);
    this.buffer = this.buffer.subarray(count);
    return head;
  }

  async until(delimiter: string, limit: number): Promise<Buffer> {
    const needle = Buffer.from(delimiter, "latin1");
    for (;;) {
      const at = this.buffer.indexOf(needle);
      if (at >= 0) {
        const head = this.buffer.subarray(0, at);
        this.buffer = this.buffer.subarray(at + needle.length);
        return head;
      }
      if (this.buffer.length > limit) {
        throw new Error(`proxy request head exceeded ${limit} bytes before ${JSON.stringify(delimiter)}`);
      }
      await this.readMore();
    }
  }

  // Callers must prepend the returned bytes before piping, or already-read data is lost.
  detach(): Buffer {
    if (this.detached) return Buffer.alloc(0);
    this.detached = true;
    this.socket.pause();
    this.socket.off("data", this.onData);
    this.socket.off("end", this.onEnd);
    this.socket.off("close", this.onEnd);
    this.socket.off("error", this.onError);
    const rest = this.buffer;
    this.buffer = Buffer.alloc(0);
    return rest;
  }

  private notify(): void {
    const wake = this.wake;
    this.wake = undefined;
    wake?.();
  }
}

// Does not vet the address; the caller must have already decided it's allowed.
export function connectTcp(address: string, port: number): Promise<Socket> {
  return new Promise((resolve, reject) => {
    const socket = connect({ host: address, port });
    socket.once("error", reject);
    socket.once("connect", () => {
      socket.off("error", reject);
      resolve(socket);
    });
  });
}

export async function connectAny(addresses: string[], port: number): Promise<Socket> {
  const attempts = addresses.map((address) => connectTcp(address, port));
  let winner: Socket;
  try {
    winner = await Promise.any(attempts);
  } catch (error) {
    throw error instanceof AggregateError ? (error.errors[0] ?? error) : error;
  }
  for (const attempt of attempts) {
    void attempt.then(
      (socket) => {
        if (socket !== winner) socket.destroy();
      },
      () => {},
    );
  }
  return winner;
}

export function pipeBothWays(client: Socket, upstream: Socket): void {
  client.pipe(upstream);
  upstream.pipe(client);
  const shutdown = (): void => {
    client.destroy();
    upstream.destroy();
  };
  client.on("error", shutdown);
  upstream.on("error", shutdown);
  client.on("close", shutdown);
  upstream.on("close", shutdown);
}
