import { connect, type Socket } from "node:net";
import { SocketReader, pipeBothWays } from "./stream";
import { denialBody, denialHeaderValue, type NetworkProtocol, type NetworkRequest, type ProxyGate } from "./types";

// Four times Node's 16 KiB default header cap: a tripwire for a client that never
// sends a blank line, never a limit a real request head reaches.
const maxHeadBytes = 65_536;

type RequestHead = { method: string; target: string; version: string; headers: Array<[string, string]> };

export async function serveHttp(client: Socket, reader: SocketReader, gate: ProxyGate): Promise<void> {
  const head = parseHead(await reader.until("\r\n\r\n", maxHeadBytes));
  const credentials = proxyCredentials(head.headers);
  const target = requestTarget(head);
  if (target === undefined) {
    writeSimple(client, 400, "Bad Request", "The proxy needs an absolute-form request URI or CONNECT.");
    return;
  }
  const protocol: NetworkProtocol = head.method === "CONNECT" ? "https_connect" : "http";
  const request: NetworkRequest = { credentials: credentials ?? "", host: target.host, port: target.port, protocol };

  if (credentials === undefined || !gate.knowsCredentials(credentials)) {
    gate.recordDenial(request, "no_credentials");
    writeUnauthenticated(client);
    return;
  }

  const verdict = await gate.authorize(request);
  if (!verdict.allowed) {
    writeDenied(client, target.host, verdict.reason);
    return;
  }

  let upstream: Socket;
  try {
    upstream = await dial(target.host, target.port);
  } catch (error) {
    writeSimple(client, 502, "Bad Gateway", `The proxy could not reach ${target.host}:${target.port}: ${String(error)}`);
    return;
  }

  const pending = reader.detach();
  if (head.method === "CONNECT") {
    client.write("HTTP/1.1 200 Connection established\r\n\r\n");
  } else {
    upstream.write(rebuildOriginForm(head, target.path));
  }
  if (pending.length > 0) upstream.write(pending);
  pipeBothWays(client, upstream);
}

function parseHead(raw: Buffer): RequestHead {
  const lines = raw.toString("latin1").split("\r\n");
  const requestLine = (lines.shift() ?? "").split(" ");
  const headers: Array<[string, string]> = [];
  for (const line of lines) {
    const at = line.indexOf(":");
    if (at < 0) continue;
    headers.push([line.slice(0, at).trim(), line.slice(at + 1).trim()]);
  }
  return { method: requestLine[0] ?? "", target: requestLine[1] ?? "", version: requestLine[2] ?? "HTTP/1.1", headers };
}

function proxyCredentials(headers: Array<[string, string]>): string | undefined {
  const header = headers.find(([name]) => name.toLowerCase() === "proxy-authorization")?.[1];
  const encoded = header?.match(/^basic\s+(\S+)$/i)?.[1];
  if (encoded === undefined) return undefined;
  const decoded = Buffer.from(encoded, "base64").toString("utf8");
  const at = decoded.indexOf(":");
  return at < 0 ? decoded : decoded.slice(0, at);
}

function requestTarget(head: RequestHead): { host: string; port: number; path: string } | undefined {
  if (head.method === "CONNECT") {
    const authority = splitAuthority(head.target);
    return authority === undefined ? undefined : { ...authority, path: "" };
  }
  const match = /^(https?):\/\/([^/?#]+)(.*)$/i.exec(head.target);
  if (!match) return undefined;
  const authority = splitAuthority(match[2] ?? "", match[1]?.toLowerCase() === "https" ? 443 : 80);
  if (authority === undefined) return undefined;
  return { ...authority, path: match[3] === "" || match[3] === undefined ? "/" : match[3] };
}

function splitAuthority(authority: string, defaultPort = 443): { host: string; port: number } | undefined {
  const withoutUserInfo = authority.slice(authority.lastIndexOf("@") + 1);
  const bracketed = /^\[([^\]]+)\](?::(\d+))?$/.exec(withoutUserInfo);
  if (bracketed) {
    const port = bracketed[2] === undefined ? defaultPort : Number(bracketed[2]);
    return bracketed[1] && validPort(port) ? { host: bracketed[1], port } : undefined;
  }
  const at = withoutUserInfo.indexOf(":");
  // An unbracketed IPv6 literal carries several colons and no port.
  if (at < 0 || withoutUserInfo.indexOf(":", at + 1) >= 0) {
    return withoutUserInfo === "" ? undefined : { host: withoutUserInfo, port: defaultPort };
  }
  const host = withoutUserInfo.slice(0, at);
  const port = Number(withoutUserInfo.slice(at + 1));
  return host !== "" && validPort(port) ? { host, port } : undefined;
}

function validPort(port: number): boolean {
  return Number.isInteger(port) && port > 0 && port <= 65_535;
}

// The hop-by-hop credentials never travel upstream, and the rewritten line is
// origin-form because the upstream is an origin server, not another proxy.
function rebuildOriginForm(head: RequestHead, path: string): string {
  const kept = head.headers.filter(([name]) => {
    const lowered = name.toLowerCase();
    return lowered !== "proxy-authorization" && lowered !== "proxy-connection";
  });
  const lines = [`${head.method} ${path} ${head.version}`, ...kept.map(([name, value]) => `${name}: ${value}`)];
  return `${lines.join("\r\n")}\r\n\r\n`;
}

function dial(host: string, port: number): Promise<Socket> {
  return new Promise((resolve, reject) => {
    const socket = connect({ host, port });
    socket.once("error", reject);
    socket.once("connect", () => {
      socket.off("error", reject);
      resolve(socket);
    });
  });
}

function writeSimple(client: Socket, status: number, reason: string, body: string): void {
  client.end(
    `HTTP/1.1 ${status} ${reason}\r\ncontent-type: text/plain\r\ncontent-length: ${Buffer.byteLength(body)}\r\nconnection: close\r\n\r\n${body}`,
  );
}

function writeUnauthenticated(client: Socket): void {
  const body = "The proxy requires the session run token as proxy credentials.";
  client.end(
    `HTTP/1.1 407 Proxy Authentication Required\r\nproxy-authenticate: Basic realm="attn"\r\ncontent-type: text/plain\r\ncontent-length: ${Buffer.byteLength(body)}\r\nconnection: close\r\n\r\n${body}`,
  );
}

function writeDenied(client: Socket, host: string, reason: Parameters<typeof denialHeaderValue>[0]): void {
  const body = denialBody(host);
  client.end(
    `HTTP/1.1 403 Forbidden\r\ncontent-type: text/plain\r\nx-proxy-error: ${denialHeaderValue(reason)}\r\ncontent-length: ${Buffer.byteLength(body)}\r\nconnection: close\r\n\r\n${body}`,
  );
}
