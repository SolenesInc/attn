import { isIPv4, type Socket } from "node:net";
import { canonicalIpLiteral } from "./policy";
import { SocketReader, pipeBothWays } from "./stream";
import type { NetworkRequest, ProxyGate } from "./types";

const version = 0x05;
const methodUsernamePassword = 0x02;
const methodNone = 0xff;
const commandConnect = 0x01;
const replySucceeded = 0x00;
const replyGeneralFailure = 0x01;
const replyNotAllowed = 0x02;
const replyHostUnreachable = 0x04;
const replyConnectionRefused = 0x05;
const replyCommandNotSupported = 0x07;
const replyAddressNotSupported = 0x08;
const addressIpv4 = 0x01;
const addressDomain = 0x03;
const addressIpv6 = 0x04;

export async function serveSocks5(client: Socket, reader: SocketReader, gate: ProxyGate): Promise<void> {
  const credentials = await negotiate(client, reader, gate);
  if (credentials === undefined) return;

  const request = await readConnectRequest(client, reader);
  if (request === undefined) return;

  const target: NetworkRequest = { credentials, host: request.host, port: request.port, protocol: "socks5_tcp" };
  const verdict = await gate.authorize(target);
  if (!verdict.allowed) {
    writeReply(client, replyNotAllowed);
    client.end();
    return;
  }

  const dialed = await gate.dial(target);
  if (dialed.outcome !== "connected") {
    writeReply(client, dialed.outcome === "denied" ? replyNotAllowed : replyForDialError(dialed.error));
    client.end();
    return;
  }
  const upstream = dialed.socket;

  const pending = reader.detach();
  writeReply(client, replySucceeded, upstream.localAddress ?? "0.0.0.0", upstream.localPort ?? 0);
  if (pending.length > 0) upstream.write(pending);
  pipeBothWays(client, upstream);
}

// RFC 1928 greeting then RFC 1929 username/password; "no authentication" is refused
// because the proxy attributes every connection to one session's proxy credentials.
async function negotiate(client: Socket, reader: SocketReader, gate: ProxyGate): Promise<string | undefined> {
  const greeting = await reader.take(2);
  if (greeting[0] !== version) {
    client.end();
    return undefined;
  }
  const methods = await reader.take(greeting[1] ?? 0);
  if (!methods.includes(methodUsernamePassword)) {
    gate.recordDenial({ credentials: "", host: "", port: 0, protocol: "socks5_tcp" }, "no_credentials");
    client.end(Buffer.from([version, methodNone]));
    return undefined;
  }
  client.write(Buffer.from([version, methodUsernamePassword]));

  const authHead = await reader.take(2);
  if (authHead[0] !== 0x01) {
    client.end(Buffer.from([0x01, 0x01]));
    return undefined;
  }
  const username = (await reader.take(authHead[1] ?? 0)).toString("utf8");
  const passwordLength = (await reader.take(1))[0] ?? 0;
  await reader.take(passwordLength);
  if (!gate.knowsCredentials(username)) {
    gate.recordDenial({ credentials: username, host: "", port: 0, protocol: "socks5_tcp" }, "no_credentials");
    client.end(Buffer.from([0x01, 0x01]));
    return undefined;
  }
  client.write(Buffer.from([0x01, 0x00]));
  return username;
}

async function readConnectRequest(
  client: Socket,
  reader: SocketReader,
): Promise<{ host: string; port: number } | undefined> {
  const head = await reader.take(4);
  if (head[0] !== version) {
    client.end();
    return undefined;
  }
  if (head[1] !== commandConnect) {
    writeReply(client, replyCommandNotSupported);
    client.end();
    return undefined;
  }
  const host = await readAddress(reader, head[3] ?? 0);
  if (host === undefined) {
    writeReply(client, replyAddressNotSupported);
    client.end();
    return undefined;
  }
  const port = (await reader.take(2)).readUInt16BE(0);
  return { host, port };
}

async function readAddress(reader: SocketReader, type: number): Promise<string | undefined> {
  if (type === addressIpv4) return [...(await reader.take(4))].join(".");
  if (type === addressDomain) return (await reader.take((await reader.take(1))[0] ?? 0)).toString("utf8");
  if (type !== addressIpv6) return undefined;
  const raw = await reader.take(16);
  const groups: string[] = [];
  for (let index = 0; index < 16; index += 2) groups.push(raw.readUInt16BE(index).toString(16));
  // The wire carries all eight groups; canonicalize so a deny rule written the short
  // way still matches what SOCKS5 hands us.
  return canonicalIpLiteral(groups.join(":")) ?? groups.join(":");
}

function writeReply(client: Socket, reply: number, address = "0.0.0.0", port = 0): void {
  const bound = isIPv4(address)
    ? Buffer.concat([Buffer.from([addressIpv4]), Buffer.from(address.split(".").map(Number))])
    : Buffer.concat([Buffer.from([addressIpv4]), Buffer.from([0, 0, 0, 0])]);
  const tail = Buffer.alloc(2);
  tail.writeUInt16BE(port & 0xff_ff, 0);
  client.write(Buffer.concat([Buffer.from([version, reply, 0x00]), bound, tail]));
}

function replyForDialError(error: unknown): number {
  const code = (error as NodeJS.ErrnoException | undefined)?.code;
  if (code === "ECONNREFUSED") return replyConnectionRefused;
  if (code === "ENOTFOUND" || code === "EAI_AGAIN" || code === "EHOSTUNREACH") return replyHostUnreachable;
  return replyGeneralFailure;
}
