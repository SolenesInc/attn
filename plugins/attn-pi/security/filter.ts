import { StringDecoder } from "node:string_decoder";
import { credentialPatterns } from "./credential-patterns";

const sensitiveName = /API_KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL|PRIVATE_KEY|PASSPHRASE|_AUTH$|^AUTH_|_AUTH_|OAUTH|_KEY$|CONNECTION_STRING|DATABASE_URL|DSN/i;
const startupName = /^(BASH_ENV|ENV|SHELLOPTS|BASHOPTS|ZDOTDIR|NODE_OPTIONS|NODE_PATH|LD_PRELOAD|DYLD_.*|BASH_FUNC_.*)$/;
const privateKeyStart = /-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----/;
const privateKeyEnd = /-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----/;

export class CredentialFilter {
  private readonly known = new Set<string>();

  constructor(env: NodeJS.ProcessEnv = {}) {
    for (const [name, value] of Object.entries(env)) {
      if (value && sensitiveName.test(name) && name !== "SSH_AUTH_SOCK") this.remember(value);
    }
  }

  remember(value: string | undefined): void {
    if (value) this.known.add(value);
  }

  environment(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
    const clean: NodeJS.ProcessEnv = {};
    for (const [name, value] of Object.entries(env)) {
      if (sensitiveName.test(name) && name !== "SSH_AUTH_SOCK") {
        this.remember(value);
      } else if (!startupName.test(name)) {
        clean[name] = value;
      }
    }
    return clean;
  }

  text(text: string, preserveLines = false): string {
    const replacement = (value: string, label: string) => label + (preserveLines ? "\n".repeat(value.split("\n").length - 1) : "");
    let clean = text.replace(/-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----[\s\S]*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|$)/g, (value) => replacement(value, "[REDACTED private key]"));
    for (const value of [...this.known].sort((a, b) => b.length - a.length)) clean = clean.split(value).join(replacement(value, "[REDACTED credential]"));
    for (const pattern of credentialPatterns) clean = clean.replace(pattern, (value) => replacement(value, "[REDACTED credential]"));
    return clean;
  }

  value<T>(value: T): T {
    if (typeof value === "string") return this.text(value) as T;
    if (Array.isArray(value)) return value.map((item) => this.value(item)) as T;
    if (!value || typeof value !== "object" || Object.getPrototypeOf(value) !== Object.prototype) return value;
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, this.value(item)])) as T;
  }

  request(payload: unknown): unknown {
    if (!payload || typeof payload !== "object" || Array.isArray(payload)) return payload;
    const clean = { ...payload } as Record<string, unknown>;
    for (const key of ["messages", "input", "instructions", "system", "contents", "systemInstruction"]) {
      if (key in clean) clean[key] = this.value(clean[key]);
    }
    return clean;
  }
}

// A line above 64 KiB is withheld, not partially emitted: a split token must never escape.
export const filteredLineLimit = 64 * 1024;

export class FilteredStream {
  private readonly decoder = new StringDecoder("utf8");
  private pending = "";
  private droppingLine = false;
  private privateKey = false;

  constructor(private readonly filter: CredentialFilter, private readonly emit: (data: Buffer) => void) {}

  write(data: Buffer): void {
    this.accept(this.decoder.write(data));
  }

  finish(): void {
    this.accept(this.decoder.end());
    if (this.pending && !this.droppingLine) this.line(this.pending);
    this.pending = "";
  }

  private accept(text: string): void {
    for (const part of text.match(/[^\n]*\n|[^\n]+$/g) ?? []) {
      const complete = part.endsWith("\n");
      if (!this.droppingLine) {
        this.pending += part;
        if (this.pending.length > filteredLineLimit) {
          if (privateKeyStart.test(this.pending)) this.privateKey = !privateKeyEnd.test(this.pending);
          this.emit(Buffer.from("[REDACTED oversized output line]\n"));
          this.pending = "";
          this.droppingLine = true;
        }
      }
      if (complete) {
        if (!this.droppingLine) this.line(this.pending);
        this.pending = "";
        this.droppingLine = false;
      }
    }
  }

  private line(line: string): void {
    if (this.privateKey) {
      if (privateKeyEnd.test(line)) this.privateKey = false;
      return;
    }
    const start = privateKeyStart.exec(line);
    if (start) {
      this.emit(Buffer.from(this.filter.text(line.slice(0, start.index)) + "[REDACTED private key]\n"));
      this.privateKey = !privateKeyEnd.test(line.slice(start.index));
      return;
    }
    this.emit(Buffer.from(this.filter.text(line)));
  }
}

const filterKey = Symbol.for("attn:pi-credential-filter");
const shared = globalThis as typeof globalThis & { [filterKey]?: CredentialFilter };
export const credentials = shared[filterKey] ??= new CredentialFilter(process.env);
