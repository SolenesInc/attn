import { decodeBinaryFrame } from '@attn/pty/binaryPtyFrame'
import { PROTOCOL_VERSION } from '@attn/types/protocolVersion'
import type {
  GetScreenSnapshotResultMessage,
  SessionObject,
} from '@attn/types/generated'
import { mark } from './metrics'

export type SpikeSession = Pick<SessionObject, 'id' | 'label' | 'state' | 'agent' | 'directory'>

type EventListener = (event: Record<string, any>) => void
type PtyListener = (seq: number, data: Uint8Array) => void

export class DaemonConnection {
  private ws: WebSocket | null = null
  private readonly eventListeners = new Set<EventListener>()
  private readonly ptyListeners = new Map<string, PtyListener>()
  private readonly waiters: Array<{ match: (e: Record<string, any>) => boolean; resolve: (e: Record<string, any>) => void }> = []
  readonly terminalBySession = new Map<string, string>()
  bytesIn = 0
  framesIn = 0

  connect(url: string, token: string): Promise<SpikeSession[]> {
    this.close()
    mark('ws_connect_start')
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'
      this.ws = ws
      let settled = false
      ws.onopen = () => {
        mark('ws_open')
        ws.send(JSON.stringify({
          cmd: 'client_hello',
          client_kind: 'phone-spike',
          version: `protocol-${PROTOCOL_VERSION}`,
          capabilities: ['binary_pty_output'],
          client_token: token,
        }))
      }
      ws.onerror = () => {
        if (!settled) { settled = true; reject(new Error(`WebSocket error connecting to ${url}`)) }
      }
      ws.onclose = (ev) => {
        if (!settled) { settled = true; reject(new Error(`WebSocket closed (${ev.code}) ${ev.reason}`)) }
        for (const l of this.eventListeners) l({ event: '__closed', code: ev.code, reason: ev.reason })
      }
      ws.onmessage = (ev) => {
        if (ev.data instanceof ArrayBuffer) {
          this.bytesIn += ev.data.byteLength
          this.framesIn += 1
          const frame = decodeBinaryFrame(ev.data)
          if (frame?.kind === 'pty_output') this.ptyListeners.get(frame.id)?.(frame.seq, frame.data)
          return
        }
        this.bytesIn += (ev.data as string).length
        const msg = JSON.parse(ev.data as string) as Record<string, any>
        if (msg.event === 'initial_state' || msg.event === 'terminal_bindings_updated') {
          this.terminalBySession.clear()
          for (const b of msg.terminal_bindings ?? []) this.terminalBySession.set(b.session_id, b.terminal_id)
        }
        if (msg.event === 'initial_state' && !settled) {
          mark('initial_state')
          settled = true
          resolve((msg.sessions ?? []).map(toSpikeSession))
        }
        if (msg.event === 'command_error' && !settled) {
          settled = true
          reject(new Error(msg.error ?? 'command_error'))
        }
        for (let i = this.waiters.length - 1; i >= 0; i -= 1) {
          if (this.waiters[i].match(msg)) {
            this.waiters[i].resolve(msg)
            this.waiters.splice(i, 1)
          }
        }
        for (const l of this.eventListeners) l(msg)
      }
    })
  }

  close(): void {
    this.ws?.close()
    this.ws = null
  }

  onEvent(listener: EventListener): () => void {
    this.eventListeners.add(listener)
    return () => this.eventListeners.delete(listener)
  }

  send(payload: Record<string, unknown>): void {
    if (this.ws?.readyState !== WebSocket.OPEN) throw new Error('daemon socket is not open')
    this.ws.send(JSON.stringify(payload))
  }

  private waitFor(match: (e: Record<string, any>) => boolean): Promise<Record<string, any>> {
    return new Promise((resolve) => this.waiters.push({ match, resolve }))
  }

  async attach(id: string, onBytes: PtyListener): Promise<Record<string, any>> {
    this.ptyListeners.set(id, onBytes)
    const result = this.waitFor((e) => e.event === 'attach_result' && e.id === id)
    this.send({ cmd: 'attach_session', id, attach_policy: 'fresh_spawn' })
    return result
  }

  detach(id: string): void {
    this.ptyListeners.delete(id)
    try { this.send({ cmd: 'detach_session', id }) } catch { /* socket gone */ }
  }

  async screenSnapshot(id: string): Promise<GetScreenSnapshotResultMessage> {
    const result = this.waitFor((e) => e.event === 'get_screen_snapshot_result' && e.id === id)
    this.send({ cmd: 'get_screen_snapshot', id })
    return result as Promise<GetScreenSnapshotResultMessage>
  }

  input(id: string, data: string): void {
    this.send({ cmd: 'pty_input', id, data, source: 'user' })
  }

  resize(id: string, cols: number, rows: number): void {
    this.send({ cmd: 'pty_resize', id, cols, rows })
  }
}

export function toSpikeSession(s: SessionObject): SpikeSession {
  return { id: s.id, label: s.label, state: s.state, agent: s.agent, directory: s.directory }
}

export const daemon = new DaemonConnection()
