import { homedir } from "node:os";
import { join } from "node:path";
import type { Theme } from "@earendil-works/pi-coding-agent";
import { Input, SelectList, sliceByColumn, truncateToWidth, visibleWidth, wrapTextWithAnsi, type Component, type Focusable } from "@earendil-works/pi-tui";
import { cachePath } from "./caches";
import { credentials } from "./filter";
import { expand, type SecurityConfig, type SecurityPolicy } from "./policy";

export type SecuritySnapshot = {
  config: SecurityConfig;
  policy?: SecurityPolicy;
  problem?: string;
  configPath: string;
  cwd: string;
  reviewAvailable: boolean;
};
type PathGroup = "caches" | "allowWrite" | "denyRead" | "denyWrite";
type Page = { kind: "main" } | { kind: "paths"; group: PathGroup } |
  { kind: "path"; group: PathGroup; path: string; fixed: boolean } |
  { kind: "input"; group: PathGroup; previous?: string } |
  { kind: "effective" } | { kind: "info"; title: string; text: string } | { kind: "reset" };
type Row = { id: string; label: string; help: string; action: () => void };
const groups = {
  caches: { title: "Cache directories", add: "caches add", remove: "caches remove", help: "Writable when build-cache access is on. Missing directories are created. Removing an entry keeps its files." },
  allowWrite: { title: "Extra writable directories", add: "allow-write", remove: "revoke-write", help: "Persistent grants for existing directories. Protected paths still take precedence. Removing a grant keeps its files." },
  denyRead: { title: "Protected reads", add: "protect-read", remove: "unprotect-read", help: "Tools cannot read these paths while the sandbox is on. Auto mode cannot override these protections." },
  denyWrite: { title: "Protected writes", add: "protect-write", remove: "unprotect-write", help: "Tools cannot change these paths while the sandbox is on, even inside a writable directory. Auto mode cannot override this." },
} as const;
const clean = (text: string) => credentials.text(text).replace(/[\x00-\x1f\x7f]/g, " ");
const shortPath = (path: string) => path.startsWith(`${homedir()}/`) ? `~/${path.slice(homedir().length + 1)}` : path;

export class SecurityPanel implements Component, Focusable {
  focused = true;
  private page: Page = { kind: "main" };
  private parents: { page: Page; selection: string | undefined }[] = [];
  private list!: SelectList;
  private rows: Row[] = [];
  private input?: Input;
  private busy = false;
  private pending = Promise.resolve();
  private message = "Changes apply immediately and persist for future sessions.";
  private error = false;
  private visibleRows = 0;

  constructor(private snapshot: SecuritySnapshot, private theme: Theme, private height: () => number,
    private redraw: () => void, private apply: (commands: string[]) => Promise<SecuritySnapshot>, private done: () => void) {
    this.rebuild();
  }

  invalidate(): void { this.list.invalidate(); this.input?.invalidate(); }

  handleInput(data: string): Promise<void> {
    if (this.busy) return this.pending;
    if (this.input) this.input.handleInput(data);
    else this.list.handleInput(data);
    this.redraw();
    return this.pending;
  }

  render(width: number): string[] {
    const inner = Math.max(1, Math.min(92, width - 4));
    if (this.visibleRows !== this.rowLimit()) this.rebuild(this.list.getSelectedItem()?.value);
    const { config, problem, reviewAvailable } = this.snapshot;
    const title = this.page.kind === "main" ? "Security" : `Security / ${this.title()}`;
    const state = problem ? "Tools blocked by a settings error" : `Sandbox ${config.enabled ? "on" : "off"} · Credentials filtered`;
    const lines = [this.theme.fg("border", "─".repeat(inner)), this.theme.fg("accent", this.theme.bold(title)),
      this.theme.fg(problem || !config.enabled ? "warning" : "muted", state), ""];
    if (this.input) {
      lines.push(this.theme.fg("muted", "Absolute path, ~/path, or relative to this session:"), ...this.input.render(inner));
    } else lines.push(...this.list.render(inner));
    const help = this.input ? `Session directory: ${this.snapshot.cwd}` : this.rows.find((row) => row.id === this.list.getSelectedItem()?.value)?.help ?? "";
    const reviewLine = this.page.kind === "main" && this.height() >= 24;
    const message = wrapTextWithAnsi(clean(this.busy ? "Applying settings…" : this.snapshot.problem ?? this.message), inner).slice(0, 2);
    const helpLines = Math.max(1, this.height() - 2 - lines.length - message.length - 5 - Number(reviewLine));
    lines.push("", ...wrapTextWithAnsi(clean(help), inner).slice(0, Math.min(4, helpLines)).map((line) => this.theme.fg("muted", line)));
    if (reviewLine) lines.push(this.theme.fg("dim", reviewAvailable && config.enabled
      ? "Extra access: auto mode reviews each requested command."
      : `Extra access: ${config.enabled ? "enable auto mode with a reviewer" : "sandbox is off"}.`));
    lines.push("", ...message.map((line) => this.theme.fg(this.error || this.snapshot.problem ? "error" : "dim", line)),
      this.theme.fg("muted", this.input ? "Enter save · Esc cancel" : `↑↓ · Enter · Esc ${this.parents.length ? "back" : "close"}`),
      this.theme.fg("border", "─".repeat(inner)));
    return lines.map((line) => `  ${truncateToWidth(line, inner)}`).slice(0, Math.max(1, this.height() - 2));
  }

  private rowLimit(): number { return Math.max(2, Math.min(10, this.height() - 17)); }
  private title(): string {
    const page = this.page;
    if (page.kind === "paths" || page.kind === "path") return groups[page.group].title;
    if (page.kind === "input") return `${page.previous ? "Edit" : "Add"} path`;
    if (page.kind === "info") return page.title;
    if (page.kind === "reset") return "Restore cache preset";
    return "Effective access";
  }

  private open(page: Page): void {
    this.parents.push({ page: this.page, selection: this.list.getSelectedItem()?.value });
    this.page = page;
    this.message = "Changes apply immediately.";
    this.error = false;
    this.rebuild();
  }

  private back(): void {
    const parent = this.parents.pop();
    if (!parent) { this.done(); return; }
    this.page = parent.page;
    this.error = false;
    this.message = "Changes apply immediately.";
    this.rebuild(parent.selection);
  }

  private save(commands: string[], after?: () => void): void {
    this.busy = true;
    this.error = false;
    this.pending = (async () => {
      try {
        this.snapshot = await this.apply(commands);
        after?.();
        this.message = "Saved. Active in this session.";
        this.rebuild(this.list.getSelectedItem()?.value);
      } catch (error) {
        this.message = error instanceof Error ? error.message : String(error);
        this.error = true;
      } finally { this.busy = false; this.redraw(); }
    })();
  }

  private paths(group: PathGroup): string[] {
    return group === "caches" ? this.snapshot.config.buildCaches.paths : this.snapshot.config[group];
  }

  private fixedPaths(): string[] {
    return [this.snapshot.configPath, join(this.snapshot.cwd, ".pi"), join(this.snapshot.cwd, ".agents")].map((path) => expand(path, this.snapshot.cwd));
  }

  private pathHelp(group: PathGroup, path: string): string {
    if (group !== "caches") return `${path}\n${groups[group].help}`;
    const { config, policy } = this.snapshot;
    if (!config.enabled || !config.buildCaches.enabled) return `${path}\nInactive while ${config.enabled ? "build-cache access" : "the sandbox"} is off.`;
    const problem = policy?.unavailableCaches.find((entry) => entry.startsWith(`${cachePath(path)}:`));
    return problem ? `Unavailable: ${problem}` : `${path}\nWritable now. Protected descendants stay protected.`;
  }

  private rebuild(selection?: string): void {
    this.visibleRows = this.rowLimit();
    const page = this.page;
    this.rows = this.makeRows();
    this.list = new SelectList(this.rows.map((row) => ({ value: row.id, label: clean(row.label) })), this.visibleRows, {
      selectedPrefix: (text) => this.theme.fg("accent", text),
      selectedText: (text) => this.theme.fg("accent", this.theme.bold(text)),
      description: (text) => this.theme.fg("muted", text), scrollInfo: (text) => this.theme.fg("dim", text), noMatch: (text) => text,
    }, { truncatePrimary: ({ text, maxWidth, item }) => {
      if (!item.value.startsWith("path-") && !item.value.startsWith("effective-")) return truncateToWidth(text, maxWidth, "…");
      if (visibleWidth(text) <= maxWidth) return text;
      const head = Math.floor((maxWidth - 1) / 3);
      return `${sliceByColumn(text, 0, head)}…${sliceByColumn(text, visibleWidth(text) - (maxWidth - head - 1), maxWidth - head - 1)}`;
    } });
    this.list.setSelectedIndex(Math.max(0, this.rows.findIndex((row) => row.id === selection)));
    this.list.onSelect = (item) => this.rows.find((row) => row.id === item.value)?.action();
    this.list.onCancel = () => this.back();
    if (page.kind === "input") {
      if (this.input) return;
      this.input = new Input();
      this.input.focused = this.focused;
      this.input.setValue(page.previous ?? "");
      this.input.handleInput("\x05");
      this.input.onEscape = () => this.back();
      this.input.onSubmit = (value) => {
        if (!value.trim()) { this.message = "Enter a path, or press Esc to cancel."; this.error = true; return; }
        const commands = page.previous ? [`${groups[page.group].remove} ${page.previous}`] : [];
        commands.push(`${groups[page.group].add} ${value}`);
        this.save(commands, () => {
          this.back();
          if (this.page.kind === "path") this.back();
        });
      };
    } else this.input = undefined;
  }

  private makeRows(): Row[] {
    const { config, policy } = this.snapshot;
    const page = this.page;
    const back: Row = { id: "back", label: "Back", help: "Return to security settings.", action: () => this.back() };
    const info = (title: string, text: string) => this.open({ kind: "info", title, text });
    const groupRow = (group: PathGroup): Row => ({ id: group, label: `${groups[group].title} · ${this.paths(group).length}${group === "denyWrite" ? " + built-in" : ""} ›`,
      help: groups[group].help, action: () => this.open({ kind: "paths", group }) });
    if (page.kind === "main") return [
      { id: "sandbox", label: `OS sandbox · ${config.enabled ? "on" : "off"}`, help: "Contains built-in tools and !/!! commands. Turning it off removes filesystem and network restrictions; credential filtering stays on.", action: () => this.save([config.enabled ? "off" : "on"]) },
      { id: "network", label: `Tool network · ${config.network === "allow" ? "allowed" : "blocked"}${config.enabled ? "" : " (inactive)"}`, help: "Blocks tool connections, including localhost. Pi can still reach your model provider. Auto mode can review network access for one command.", action: () => this.save([`network ${config.network === "allow" ? "deny" : "allow"}`]) },
      { id: "cacheAccess", label: `Build-cache access · ${config.buildCaches.enabled ? "on" : "off"}${config.enabled ? "" : " (inactive)"}`, help: "Lets build tools write configured caches without requesting extra access. Turning this off preserves cached files and other write grants.", action: () => this.save([`caches ${config.buildCaches.enabled ? "off" : "on"}`]) },
      groupRow("caches"), groupRow("allowWrite"), groupRow("denyRead"), groupRow("denyWrite"),
      { id: "effective", label: "Effective access ›", help: "Inspect current write grants, protected paths and unavailable caches.", action: () => this.open({ kind: "effective" }) },
      { id: "filter", label: "Credential filtering · always on", help: "Filters sensitive environment variables and recognized secrets in tool output and model requests. It stays on when the sandbox or auto mode is off.", action: () => info("Credential filtering", "Credential filtering is always on. It removes sensitive environment variables from tools and redacts recognized secrets in text output and model requests. Images, encoded data and unrecognized secrets are not detected.") },
      { id: "review", label: `Extra access review · ${config.enabled && this.snapshot.reviewAvailable ? "available" : "unavailable"}`, help: "Auto mode reviews the command and requested access together. Approval lasts for one execution and never overrides protected paths.", action: () => info("Extra access review", config.enabled && this.snapshot.reviewAvailable ? "Auto mode can approve temporary write or network access for one command. It uses your existing auto-mode policy and classifier. A refusal tells the agent why and how to ask you for approval." : "Extra access review requires the sandbox and auto mode with a configured reviewer. /auto on enables auto mode; /auto status shows its configuration. Persistent grants can be managed in Extra writable directories.") },
      { id: "file", label: "Settings file ›", help: this.snapshot.configPath, action: () => info("Settings file", `${this.snapshot.configPath}\nSettings apply immediately here and are loaded by future sessions. Other running sessions keep their current policy. Extensions and MCP servers remain trusted code outside this sandbox.`) },
      { id: "close", label: "Close", help: "All changes are already saved.", action: () => this.done() },
    ];
    if (page.kind === "paths") {
      const fixed = page.group === "denyWrite" ? this.fixedPaths() : [];
      const paths = [...new Set([...this.paths(page.group), ...fixed])];
      return [
        { id: "add", label: "+ Add path", help: groups[page.group].help, action: () => this.open({ kind: "input", group: page.group }) },
        ...(page.group === "caches" ? [{ id: "reset", label: "Restore standard cache preset…", help: "Replaces this list with the standard paths for this platform and enables build-cache access.", action: () => this.open({ kind: "reset" } as Page) }] : []),
        ...paths.map((path, i): Row => {
          const isFixed = fixed.includes(expand(path, homedir()));
          const unavailable = page.group === "caches" && policy?.unavailableCaches.some((entry) => entry.startsWith(`${cachePath(path)}:`));
          return { id: `path-${i}`, label: `${unavailable ? "! " : ""}${shortPath(path)}${isFixed ? " (built-in)" : ""}`,
            help: isFixed ? `${path}\nBuilt-in write protection. This cannot be removed while the sandbox is on.` : this.pathHelp(page.group, path),
            action: () => this.open({ kind: "path", group: page.group, path, fixed: isFixed }) };
        }), back,
      ];
    }
    if (page.kind === "path") {
      const help = page.fixed ? `${page.path}\nBuilt-in write protection cannot be removed while the sandbox is on.` : this.pathHelp(page.group, page.path);
      return page.fixed ? [{ ...back, help }] : [
        { id: "edit", label: "Edit path…", help, action: () => this.open({ kind: "input", group: page.group, previous: page.path }) },
        { id: "remove", label: "Remove from this list", help: `${page.path}\n${page.group.startsWith("deny") ? "Removes this protection. Other protections still apply." : "Removes this grant; other grants still apply. Files are kept."}`, action: () => this.save([`${groups[page.group].remove} ${page.path}`], () => this.back()) }, back,
      ];
    }
    if (page.kind === "reset") return [
      { id: "cancel", label: "Keep current cache paths", help: "Your current cache settings will stay as they are.", action: () => this.back() },
      { id: "reset", label: "Restore and enable standard preset", help: "Replaces all customized cache entries. Existing files are kept. Standard directories are created as needed.", action: () => this.save(["caches reset"], () => this.back()) },
    ];
    if (page.kind === "info") return [{ ...back, help: page.text }];
    if (page.kind === "effective") {
      if (!config.enabled) return [{ ...back, help: "The OS sandbox is off. Filesystem and network access are unrestricted; credential filtering stays on." }];
      const entries = [
        ...(policy?.unavailableCaches ?? []).map((path) => ({ label: "Unavailable cache", path })),
        ...(policy?.allowWrite ?? []).map((path) => ({ label: "Writable", path })),
        ...(policy?.denyRead ?? []).map((path) => ({ label: "Read protected", path })),
        ...(policy?.denyWrite ?? []).map((path) => ({ label: "Write protected", path })),
      ];
      return [...entries.map(({ label, path }, i): Row => ({ id: `effective-${i}`, label: `${label} · ${shortPath(path)}`, help: path,
        action: () => info(label, path) })), back];
    }
    return [];
  }
}
