import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toBase64Url } from './bytes'
import type { DeviceKeys } from './device'
import {
  pingIntervalMs,
  pongDeadlineMs,
  runLink,
  terminalCreditBytes,
  type Link,
  type LinkEvent,
} from './link'
import { FakeRelay, socketsOf } from './test/fakeRelay'
import { holdDecryption } from './test/nobleCrypto'

let relay: FakeRelay
let events: Array<LinkEvent>
let close: () => void
let link: Link

const signSecret = ed25519.utils.randomSecretKey()
const keys: DeviceKeys = {
  signSecret,
  signPub: ed25519.getPublicKey(signSecret),
  noiseSecret: x25519.utils.randomSecretKey(),
  id: 'device',
  name: 'Test iPhone',
}

const connect = async () => {
  link = runLink(
    {
      cloud: 'https://cloud.example',
      homeId: 'home',
      homeNoisePub: toBase64Url(relay.homeNoisePub),
      homeName: 'Mac',
      profileId: 'p',
    },
    keys,
    (event) => events.push(event),
  )
  close = link.close
  await vi.advanceTimersByTimeAsync(1)
}

const tags = () => events.map((event) => event._tag)

beforeEach(() => {
  vi.useFakeTimers()
  relay = new FakeRelay()
  events = []
  vi.stubGlobal('WebSocket', socketsOf(relay))
})

afterEach(() => {
  close()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('a phone keeps checking that its home still answers', () => {
  it('stays live for as long as the home answers its pings', async () => {
    await connect()
    expect(tags()).toEqual(['Welcomed', 'Sessions'])

    await vi.advanceTimersByTimeAsync(10 * pingIntervalMs)

    expect(relay.homePings).toBe(10)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
    expect(relay.sockets).toHaveLength(1)
  })

  it('reconnects when a home stops answering although its relay still does', async () => {
    await connect()
    relay.homeAnswersPings = false

    await vi.advanceTimersByTimeAsync(pingIntervalMs + pongDeadlineMs - 1000)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])

    await vi.advanceTimersByTimeAsync(1000)
    expect(events.at(-1)).toEqual({ _tag: 'Lost', reason: 'Mac: no answer for 10 s; reconnecting' })
    expect(relay.sockets[0]!.closed).toBe(true)

    relay.homeAnswersPings = true
    await vi.advanceTimersByTimeAsync(100)
    expect(relay.sockets).toHaveLength(2)
    expect(tags().slice(-2)).toEqual(['Welcomed', 'Sessions'])
  })

  it('pings only the relay when the home serves api 1, which has no ping', async () => {
    relay.welcomeApi = 1
    relay.homeAnswersPings = false
    await connect()

    await vi.advanceTimersByTimeAsync(10 * pingIntervalMs)

    expect(relay.homePings).toBe(0)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
  })
})

describe('a phone that closes its link', () => {
  it('reports nothing the home sent once it has closed, even when that message was still being decrypted', async () => {
    await connect()
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
    const release = holdDecryption()
    relay.sessions(relay.sockets[0]!, [])
    await vi.advanceTimersByTimeAsync(1)

    close()
    release()
    await vi.advanceTimersByTimeAsync(1)

    expect(tags()).toEqual(['Welcomed', 'Sessions'])
  })
})

describe('a terminal the phone opened', () => {
  const sink = () => {
    const shown: Array<string> = []
    return {
      shown,
      sink: {
        screen: (_: unknown, ansi: Uint8Array) => shown.push(`screen:${new TextDecoder().decode(ansi)}`),
        output: (data: Uint8Array) => shown.push(new TextDecoder().decode(data)),
        resized: (cols: number, rows: number) => shown.push(`resized:${cols}x${rows}`),
        ended: () => shown.push('ended'),
        failed: (message: string) => shown.push(`failed:${message}`),
      },
    }
  }
  const attachCommands = () =>
    relay.commands.filter((command) => command.cmd === 'terminal_attach' || command.cmd === 'terminal_focus')

  it('attaches with its size and credit, shows what the home sends, and attaches again after a reconnect', async () => {
    await connect()
    const { shown, sink: terminal } = sink()
    link.openTerminal('t1', { cols: 49, rows: 35 }, terminal)
    await vi.advanceTimersByTimeAsync(1)
    expect(attachCommands()).toEqual([
      { cmd: 'terminal_attach', terminal_id: 't1', cols: 49, rows: 35, credit: terminalCreditBytes },
      { cmd: 'terminal_focus', terminal_id: 't1' },
    ])

    const socket = relay.sockets[0]!
    socket.toPhone({
      event: 'terminal_screen',
      terminal_id: 't1',
      cols: 49,
      rows: 35,
      ansi: btoa('$ '),
      seq: 4,
    })
    socket.output('t1', 5, 'ls\r\n')
    socket.output('t2', 6, 'not this terminal')
    socket.toPhone({ event: 'terminal_resized', terminal_id: 't1', cols: 120, rows: 36 })
    await vi.advanceTimersByTimeAsync(1)
    expect(shown).toEqual(['screen:$ ', 'ls\r\n', 'resized:120x36'])

    relay.homeAnswersPings = false
    await vi.advanceTimersByTimeAsync(pingIntervalMs + pongDeadlineMs)
    relay.homeAnswersPings = true
    await vi.advanceTimersByTimeAsync(100)
    expect(relay.sockets).toHaveLength(2)
    expect(attachCommands()).toHaveLength(4)
    expect(attachCommands().at(-2)).toMatchObject({
      cmd: 'terminal_attach',
      terminal_id: 't1',
      cols: 49,
      rows: 35,
    })
  })

  it('tells the terminal why when the home is too old to open it', async () => {
    relay.welcomeApi = 3
    await connect()
    const { shown, sink: terminal } = sink()
    link.openTerminal('t1', { cols: 49, rows: 35 }, terminal)
    await vi.advanceTimersByTimeAsync(1)
    expect(attachCommands()).toEqual([])
    expect(shown).toEqual([
      'failed:attn on Mac is too old to open terminals from a phone; update it to the latest attn',
    ])
  })
})
