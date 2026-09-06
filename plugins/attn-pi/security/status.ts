import type { Theme } from "@earendil-works/pi-coding-agent";

// pi dims its own footer lines but renders extension status lines unstyled, so this
// surface tints its own text. Only the TUI has a theme; other modes take it verbatim.
export function statusTheme(ctx: { mode?: string; ui?: { theme?: Theme } }): Theme | undefined {
  return ctx.mode === "tui" ? ctx.ui?.theme : undefined;
}

export function dimmed(theme: Theme | undefined, text: string): string {
  return theme?.fg("dim", text) ?? text;
}

export function problem(theme: Theme | undefined, text: string): string {
  return theme?.fg("error", text) ?? text;
}
