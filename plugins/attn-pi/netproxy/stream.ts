import { connect, type Socket } from "node:net";

export class ConnectionClosedError extends Error {
  constructor(message = "the proxy client closed the connection") {
    super(message);
    this.name = "ConnectionClosedError";
  }
}

/** Reads a socket by demand while a protocol handshake is parsed, then hands the
 * socket (and whatever it has buffered) back for piping. */
export class SocketReader {
  private buffer: Buffer = Buffer.alloc(0);
  private ended = false;
  private failure: Error | undefined;
  private wake: (() => void) | undefined;
  private detached = false;

  private readonly onData = (chunk: Buffer): void => {
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
    socket.on("data", this.onData);
    socket.on("end", this.onEnd);
    socket.on("close", this.onEnd);
    socket.on("error", this.onError);
  }

  /** Resolves once at least `count` bytes are buffered. */
  async need(count: number): Promise<void> {
    while (this.buffer.length < count) {
      if (this.failure) throw this.failure;
      if (this.ended) throw new ConnectionClosedError();
      await new Promise<void>((resolve) => {
        this.wake = resolve;
      });
    }
  }

  /** Resolves the first `count` buffered bytes without consuming them. */
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

  /** Reads up to and including `delimiter`, returning the bytes before it. */
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
      if (this.failure) throw this.failure;
      if (this.ended) throw new ConnectionClosedError();
      await new Promise<void>((resolve) => {
        this.wake = resolve;
      });
    }
  }

  /** Stops reading and returns the bytes read past the handshake. */
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

/** Connects to one already-vetted address; the caller decides which address that is. */
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

/** Joins two sockets and tears both down when either side finishes or fails. */
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
