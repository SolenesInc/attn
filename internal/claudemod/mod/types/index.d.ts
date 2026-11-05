export type Message = { id: string; text: string; user_words?: boolean; ring?: object }

export type Request = { id: string; kind: 'compact' } | ({ kind?: 'input' } & Message)

declare module 'claude-code' {
  interface PluginState {
    attn: {
      isTurnRunning: boolean
      heldLines: string[]
      waitingLines: string[]
    }
  }
}
