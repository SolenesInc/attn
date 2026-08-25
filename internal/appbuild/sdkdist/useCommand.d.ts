/** What one invocation did. Never a rejection — see the file header. */
export type CommandOutcome = {
    ok: true;
    value: unknown;
} | {
    ok: false;
    error: string;
    /** A stable name for the refusal, when attn had one. `"reconcile_owed"` is
     * worth retrying — the app is rebuilding its collections. */
    code?: string;
};
export interface CommandRunner {
    (payload?: unknown): Promise<CommandOutcome>;
    readonly pending: boolean;
    /** The last failure, meant to be shown, cleared by the next call. */
    readonly error: string | null;
}
/** Invoke one of this app's declared commands. It must appear in a `[[commands]]` block
 * of attn-app.toml and the bundle must export a handler under `commands`. */
export declare function useCommand(command: string): CommandRunner;
