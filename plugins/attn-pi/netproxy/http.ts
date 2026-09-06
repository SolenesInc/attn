import type { Socket } from "node:net";
import { SocketReader, pipeBothWays } from "./stream";
import { denialBody, denialHeaderValue, type NetworkProtocol, type NetworkRequest, type ProxyGate } from "./types";

// Four times Node's 16 KiB default header cap: a tripwire for a client that never
// sends a blank line, never a limit a real request head reaches.
const maxHeadBytes = 65_536;

type RequestHead = { method: string; target: string; version: string; headers: Array<[string, string]> };
type RequestTarget = { host: string; port: number; defaultPort: number; path: string };

// Codex's hop-by-hop set (http_proxy.rs:997-1020). `Connection` also names further
// headers that end at this hop, so its own tokens join the list.
const hopByHopHeaders = [
  "connection",
  "keep-alive",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
];

export async function serveHttp(client: Socket, reader: SocketReader, gate: ProxyGate): Promise<void> {
  const head = parseHead(await reader.until("\r\n\r\n", maxHeadBytes));
  if (head === undefined) {
    writeSimple(client, 400, "Bad Request", "The proxy could not parse the request head.");
    return;
  }
  const credentials = proxyCredentials(head.headers);
  const target = requestTarget(head);
  if (target === undefined) {
    writeSimple(client, 400, "Bad Request", "The proxy needs an absolute-form request URI or CONNECT.");
    return;
  }
  // An absolute-form target and a Host header that disagree are two different requests,
  // and the upstream would answer the one we did not authorize (http_proxy.rs:958-996).
  if (head.method !== "CONNECT" && !hostHeaderAgrees(head, target)) {
    writeSimple(client, 400, "Bad Request", "Host header does not match request target");
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

  const dialed = await gate.dial(request);
  if (dialed.outcome === "denied") {
    writeDenied(client, target.host, dialed.reason);
    return;
  }
  if (dialed.outcome === "unreachable") {
    const detail = String(dialed.error);
    writeSimple(client, 502, "Bad Gateway", `The proxy could not reach ${target.host}:${target.port}: ${detail}`);
    return;
  }
  const upstream = dialed.socket;

  const pending = reader.detach();
  if (head.method === "CONNECT") {
    client.write("HTTP/1.1 200 Connection established\r\n\r\n");
  } else {
    upstream.write(rebuildOriginForm(head, target.path));
  }
  if (pending.length > 0) upstream.write(pending);
  pipeBothWays(client, upstream);
}

function parseHead(raw: Buffer): RequestHead | undefined {
  const lines = raw.toString("latin1").split("\r\n");
  // A bare LF or CR surviving the CRLF split is a second request the upstream would
  // read as its own; refuse the message rather than forward the smuggled half.
  if (lines.some((line) => line.includes("\n") || line.includes("\r"))) return undefined;
  const requestLine = (lines.shift() ?? "").split(" ");
  const headers: Array<[string, string]> = [];
  for (const line of lines) {
    if (line.startsWith(" ") || line.startsWith("\t")) {
      const previous = headers.at(-1);
      if (previous === undefined) return undefined;
      previous[1] = `${previous[1]} ${line.trim()}`;
      continue;
    }
    const at = line.indexOf(":");
    if (at < 0) continue;
    headers.push([line.slice(0, at).trim(), line.slice(at + 1).trim()]);
  }
  return { method: requestLine[0] ?? "", target: requestLine[1] ?? "", version: requestLine[2] ?? "HTTP/1.1", headers };
}

function headerValue(headers: Array<[string, string]>, name: string): string | undefined {
  return headers.find(([candidate]) => candidate.toLowerCase() === name)?.[1];
}

/** A missing Host header is fine; a present one must name the same origin. */
function hostHeaderAgrees(head: RequestHead, target: RequestTarget): boolean {
  const header = headerValue(head.headers, "host");
  if (header === undefined) return true;
  const parsed = parseAuthority(header.trim());
  if (parsed === undefined) return false;
  if (parsed.host.toLowerCase() !== target.host.toLowerCase()) return false;
  return parsed.port === undefined ? target.port === target.defaultPort : parsed.port === target.port;
}

function proxyCredentials(headers: Array<[string, string]>): string | undefined {
  const header = headers.find(([name]) => name.toLowerCase() === "proxy-authorization")?.[1];
  const encoded = header?.match(/^basic\s+(\S+)$/i)?.[1];
  if (encoded === undefined) return undefined;
  const decoded = Buffer.from(encoded, "base64").toString("utf8");
  const at = decoded.indexOf(":");
  return at < 0 ? decoded : decoded.slice(0, at);
}

function requestTarget(head: RequestHead): RequestTarget | undefined {
  if (head.method === "CONNECT") {
    const authority = splitAuthority(head.target, 443);
    return authority === undefined ? undefined : { ...authority, defaultPort: 443, path: "" };
  }
  const match = /^(https?):\/\/([^/?#]+)(.*)$/i.exec(head.target);
  if (!match) return undefined;
  const defaultPort = match[1]?.toLowerCase() === "https" ? 443 : 80;
  const authority = splitAuthority(match[2] ?? "", defaultPort);
  if (authority === undefined) return undefined;
  // Origin form always starts at "/", and the fragment is the client's business only.
  const tail = match[3] ?? "";
  const cut = tail.indexOf("#");
  const withoutFragment = cut < 0 ? tail : tail.slice(0, cut);
  const path = withoutFragment.startsWith("/") ? withoutFragment : `/${withoutFragment}`;
  return { ...authority, defaultPort, path };
}

function splitAuthority(authority: string, defaultPort: number): { host: string; port: number } | undefined {
  const parsed = parseAuthority(authority);
  return parsed === undefined ? undefined : { host: parsed.host, port: parsed.port ?? defaultPort };
}

/** Splits an authority, saying whether it carried an explicit port at all. */
function parseAuthority(authority: string): { host: string; port?: number } | undefined {
  const withoutUserInfo = authority.slice(authority.lastIndexOf("@") + 1);
  const bracketed = /^\[([^\]]+)\](?::(\d+))?$/.exec(withoutUserInfo);
  if (bracketed) {
    if (!bracketed[1]) return undefined;
    if (bracketed[2] === undefined) return { host: bracketed[1] };
    const port = Number(bracketed[2]);
    return validPort(port) ? { host: bracketed[1], port } : undefined;
  }
  const at = withoutUserInfo.indexOf(":");
  // An unbracketed IPv6 literal carries several colons and no port.
  if (at < 0 || withoutUserInfo.indexOf(":", at + 1) >= 0) {
    return withoutUserInfo === "" ? undefined : { host: withoutUserInfo };
  }
  const host = withoutUserInfo.slice(0, at);
  const port = Number(withoutUserInfo.slice(at + 1));
  return host !== "" && validPort(port) ? { host, port } : undefined;
}

function validPort(port: number): boolean {
  return Number.isInteger(port) && port > 0 && port <= 65_535;
}

// Hop-by-hop credentials never travel upstream, and `Connection: close` replaces any
// keep-alive so a second request cannot ride this tunnel to an unchecked host.
function rebuildOriginForm(head: RequestHead, path: string): string {
  const dropped = new Set(hopByHopHeaders);
  for (const token of headerValue(head.headers, "connection")?.split(",") ?? []) {
    const name = token.trim().toLowerCase();
    if (name !== "") dropped.add(name);
  }
  const kept = head.headers.filter(([name]) => !dropped.has(name.toLowerCase()));
  const lines = [
    `${head.method} ${path} ${head.version}`,
    ...kept.map(([name, value]) => `${name}: ${value}`),
    "Connection: close",
  ];
  return `${lines.join("\r\n")}\r\n\r\n`;
}

function writeSimple(client: Socket, status: number, reason: string, body: string): void {
  client.end(
    `HTTP/1.1 ${status} ${reason}\r\ncontent-type: text/plain\r\ncontent-length: ${Buffer.byteLength(body)}\r\nconnection: close\r\n\r\n${body}`,
  );
}

function writeUnauthenticated(client: Socket): void {
  const body = "The proxy requires this session's proxy credentials.";
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
