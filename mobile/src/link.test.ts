import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toBase64Url } from './bytes'
import type { PhoneKeys } from './phoneKeys'
import {
  pingIntervalMs,
  pongDeadlineMs,
  runLink,
  terminalCreditBytes,
  type Link,
  type LinkEvent,
} from './link'
import { FakeCloudRelay, socketsOf } from './test/fakeCloudRelay'
import { holdDecryption } from './test/nobleCrypto'

let cloudRelay: FakeCloudRelay
let events: Array<LinkEvent>
let close: () => void
let link: Link

const signSecret = ed25519.utils.randomSecretKey()
const keys: PhoneKeys = {
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
      homeNoisePub: toBase64Url(cloudRelay.homeNoisePub),
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
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  cloudRelay = new FakeCloudRelay()
  events = []
  vi.stubGlobal('WebSocket', socketsOf(cloudRelay))
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

    expect(cloudRelay.homePings).toBe(10)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
    expect(cloudRelay.sockets).toHaveLength(1)
  })

  it('reconnects when a home stops answering although its Cloud Relay still does', async () => {
    await connect()
    cloudRelay.homeAnswersPings = false

    await vi.advanceTimersByTimeAsync(pingIntervalMs + pongDeadlineMs - 1000)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])

    await vi.advanceTimersByTimeAsync(1000)
    expect(events.at(-1)).toEqual({ _tag: 'Lost', reason: 'Mac: no answer for 10 s; reconnecting' })
    expect(cloudRelay.sockets[0]!.closed).toBe(true)

    cloudRelay.homeAnswersPings = true
    await vi.advanceTimersByTimeAsync(100)
    expect(cloudRelay.sockets).toHaveLength(2)
    expect(tags().slice(-2)).toEqual(['Welcomed', 'Sessions'])
  })

  it('pings only the Cloud Relay when the home serves api 1, which has no ping', async () => {
    cloudRelay.welcomeApi = 1
    cloudRelay.homeAnswersPings = false
    await connect()

    await vi.advanceTimersByTimeAsync(10 * pingIntervalMs)

    expect(cloudRelay.homePings).toBe(0)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
  })
})

describe('a phone that closes its link', () => {
  it('reports nothing the home sent once it has closed, even when that message was still being decrypted', async () => {
    await connect()
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
    const release = holdDecryption()
    cloudRelay.sessions(cloudRelay.sockets[0]!, [])
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
    cloudRelay.commands.filter(
      (command) => command.cmd === 'terminal_attach' || command.cmd === 'terminal_focus',
    )

  it('attaches with its size and credit, shows what the home sends, and attaches again after a reconnect', async () => {
    await connect()
    const { shown, sink: terminal } = sink()
    link.openTerminal('t1', { cols: 49, rows: 35 }, terminal)
    await vi.advanceTimersByTimeAsync(1)
    expect(attachCommands()).toEqual([
      { cmd: 'terminal_attach', terminal_id: 't1', cols: 49, rows: 35, credit: terminalCreditBytes },
      { cmd: 'terminal_focus', terminal_id: 't1' },
    ])

    const socket = cloudRelay.sockets[0]!
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

    cloudRelay.homeAnswersPings = false
    await vi.advanceTimersByTimeAsync(pingIntervalMs + pongDeadlineMs)
    cloudRelay.homeAnswersPings = true
    await vi.advanceTimersByTimeAsync(100)
    expect(cloudRelay.sockets).toHaveLength(2)
    expect(attachCommands()).toHaveLength(4)
    expect(attachCommands().at(-2)).toMatchObject({
      cmd: 'terminal_attach',
      terminal_id: 't1',
      cols: 49,
      rows: 35,
    })
  })

  it('tells the terminal why when the home is too old to open it', async () => {
    cloudRelay.welcomeApi = 3
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

describe('a phone keeps its home’s garden', () => {
  const seed = (id: string, title: string) => ({
    id,
    slug: title.toLowerCase().replaceAll(' ', '-'),
    title,
    status: 'planted',
    claimed: false,
    ready: true,
    waits_for: [],
    state_changed_at: '2026-11-02T10:00:00Z',
  })
  const gardens = () =>
    events.flatMap((event) =>
      event._tag === 'Garden' ? [event.seeds.map((shown) => `${shown.id}:${shown.status}`)] : [],
    )

  it('shows the open seeds in the order the home lists them, keeping the ones a change leaves alone', async () => {
    await connect()
    const socket = cloudRelay.sockets[0]!
    socket.toPhone({ event: 'garden_seeds', seeds: [seed('s-1', 'Read seeds'), seed('s-2', 'Move seeds')] })
    socket.toPhone({ event: 'garden_seeds', seeds: [seed('s-3', 'Ship it')] })
    socket.toPhone({ event: 'garden', ids: ['s-3', 's-2', 's-1'] })
    socket.toPhone({ event: 'garden_seeds', seeds: [{ ...seed('s-2', 'Move seeds'), status: 'growing' }] })
    socket.toPhone({ event: 'garden', ids: ['s-3', 's-2'] })
    await vi.advanceTimersByTimeAsync(1)
    expect(gardens()).toEqual([
      ['s-3:planted', 's-2:planted', 's-1:planted'],
      ['s-3:planted', 's-2:growing'],
    ])
  })
})
