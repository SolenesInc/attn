export type Message = { id: string; text: string }

declare module 'claude-code' {
  interface PluginState {
    attn: {
      isTurnRunning: boolean
      heldLines: string[]
      waitingLines: string[]
    }
  }
}
