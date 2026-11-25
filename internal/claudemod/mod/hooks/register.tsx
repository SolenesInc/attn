import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Message, Request } from '../types'

type Link = { terminal: string; socketPath: string }
type Outcome = 'submitted' | 'held' | 'appended' | 'interrupted' | 'refused'
type Compacted = 'compacted' | 'skipped' | 'busy' | 'ready' | 'failed'

const POLL_RETRY_MS = 1000
// Receipt (2.1.332, 22 cores): the slowest healthy connect came 2.2 s after the composer drew, with
// 66 busy loops running. The mod API gives a hook 10 s, counting $.clock waits.
const TOOLS_WAIT_MS = 8000
const RING_ID_PREFIX = 'inbox-ring/'
const BETWEEN_TURNS_ONLY = ['compacts between turns', 'a newer turn began']

const persistedTurnRunning = atom({ plugin: 'attn', key: 'isTurnRunning' } as const, false)
const persistedTurnId = atom({ plugin: 'attn', key: 'turnId' } as const, '')
const heldLines = atom({ plugin: 'attn', key: 'heldLines' } as const, [] as string[])
const waitingLines = atom({ plugin: 'attn', key: 'waitingLines' } as const, [] as string[])

const instance = crypto.randomUUID()
let link: Link = { terminal: '', socketPath: '' }
let isTurnRunning = false
let isStopped = false
let turnId = ''
let ownAbort = false
let steers: Message[] = []
let interrupts: Message[] = []
let queued: Message[] = []
let givenBack: Message[] = []
let lastRequest: Promise<void> = Promise.resolve()
let turnsEnded = 0
let compactRefused = false

function firstLine(text: string): string {
  return text.split('\n', 1)[0] ?? ''
}

function post($: EngineInterface, path: string, body: object, answered?: (text: string) => void): Promise<void> {
  lastRequest = lastRequest.then(() => send($, path, body, answered))
  return lastRequest
}

async function send($: EngineInterface, path: string, body: object, answered?: (text: string) => void): Promise<void> {
  try {
    const answer = await $.http.fetch(`http://attn${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ terminal: link.terminal, instance, ...body }),
      socketPath: link.socketPath,
    })
    if (answer.status === 200) {
      answered?.(answer.text)
    }
  } catch (err) {
    $.ui.log(`POST ${path} failed: ${String(err)}`, { to: 'debug' })
  }
}

function ack($: EngineInterface, id: string, outcome: Outcome, reason?: string): Promise<void> {
  return post($, '/claude/ack', { id, outcome, reason })
}

function ackHeld($: EngineInterface, message: Message, outcome: Outcome): Promise<void> {
  return post($, '/claude/ack', { id: message.id, outcome, ring: message.ring })
}

function sendGivenBack($: EngineInterface): Promise<void> {
  return post($, '/claude/return', { returns: givenBack }, text => {
    const { settled } = JSON.parse(text) as { settled: string[] | null }
    givenBack = givenBack.filter(message => !settled?.includes(message.id))
  })
}

function giveBack($: EngineInterface, messages: Message[]): Promise<void> {
  givenBack = [...givenBack, ...messages]
  return sendGivenBack($)
}

function setTurnRunning($: EngineInterface, value: boolean): Promise<unknown> {
  isTurnRunning = value
  const stored = update($, persistedTurnRunning, () => value)
  if (value) {
    return stored
  }
  turnsEnded++
  return Promise.all([stored, reportBetweenTurns($)])
}

function reportBetweenTurns($: EngineInterface): Promise<void> {
  if (!compactRefused) {
    return Promise.resolve()
  }
  compactRefused = false
  return post($, '/claude/compacted', { outcome: 'ready' })
}

function setQueued($: EngineInterface, messages: Message[]): Promise<unknown> {
  queued = messages
  return update($, heldLines, () => messages.map(message => firstLine(message.text)))
}

function listWaiting($: EngineInterface, messages: Message[]): Promise<unknown> {
  const rings = messages.filter(message => message.id.startsWith(RING_ID_PREFIX))
  return update($, waitingLines, lines => [...new Set([...lines, ...rings.map(ring => firstLine(ring.text))])])
}

async function submit($: EngineInterface, text: string, asUser: boolean): Promise<string | undefined> {
  try {
    const entered = await $.prompt.submit({ text, asUser })
    return 'drop' in entered ? entered.drop : undefined
  } catch (err) {
    return String(err)
  }
}

async function refuseStopped($: EngineInterface, message: Message): Promise<void> {
  await listWaiting($, [message])
  await post($, '/claude/ack', { id: message.id, outcome: 'stopped' })
}

async function place($: EngineInterface, message: Message): Promise<void> {
  if (isStopped) {
    return refuseStopped($, message)
  }
  if (isTurnRunning && message.turn === 'steer') {
    steers = [...steers, message]
    return ack($, message.id, 'held')
  }
  if (isTurnRunning && message.turn === 'interrupt') {
    interrupts = [...interrupts, message]
    await ack($, message.id, 'held')
    return abortOwnTurn($)
  }
  if (isTurnRunning) {
    await Promise.all([setQueued($, [...queued, message]), ack($, message.id, 'held')])
    return
  }
  const drop = await submit($, message.text, message.user_words === true)
  if (drop === undefined) {
    return ack($, message.id, 'submitted')
  }
  if (drop.includes('stopped')) {
    isStopped = true
    return refuseStopped($, message)
  }
  return ack($, message.id, 'refused', drop)
}

function compact($: EngineInterface, id: string): void {
  const report = (outcome: Compacted, reason?: string) => post($, '/claude/compacted', { id, outcome, reason })
  const turnsEndedWhenAsked = turnsEnded
  void $.session.compact().then(
    result => ('skip' in result ? report('skipped', String(result.skip)) : report('compacted')),
    async (err: unknown) => {
      const reason = String(err)
      if (!BETWEEN_TURNS_ONLY.some(busy => reason.includes(busy))) {
        return report('failed', reason)
      }
      await report('busy', reason)
      compactRefused = true
      if (turnsEnded !== turnsEndedWhenAsked) {
        await reportBetweenTurns($)
      }
    },
  )
}

async function pollForever($: EngineInterface): Promise<void> {
  const query = `terminal=${encodeURIComponent(link.terminal)}&instance=${encodeURIComponent(instance)}`
  for (;;) {
    if (givenBack.length > 0) {
      await sendGivenBack($)
    }
    let status = 0
    let body = ''
    try {
      const answer = await $.http.fetch(`http://attn/claude/next?${query}`, { socketPath: link.socketPath })
      status = answer.status
      body = answer.text
    } catch (err) {
      $.ui.log(`poll failed: ${String(err)}`, { to: 'debug' })
    }
    if (status === 409) {
      return
    }
    if (status === 200) {
      const request = JSON.parse(body) as Request
      if (request.kind === 'compact') {
        compact($, request.id)
      } else {
        await place($, request)
      }
    } else if (status !== 204) {
      await $.clock.sleep(POLL_RETRY_MS)
    }
  }
}

async function abortOwnTurn($: EngineInterface): Promise<void> {
  if (ownAbort) {
    return
  }
  ownAbort = true
  try {
    await $.turn.abort({ turnId })
  } catch (err) {
    ownAbort = false
    $.ui.log(`attn could not stop the turn for an interrupt, so it waits for the turn to end: ${String(err)}`, { to: 'debug' })
  }
}

async function appendSteers($: EngineInterface): Promise<void> {
  const messages = steers
  steers = []
  for (const message of messages) {
    const appended = await $.session
      .append({ message: { type: 'user', content: [{ type: 'text', text: message.text }] } })
      .catch((err: unknown) => ({ deny: String(err) }))
    if (appended.deny !== undefined) {
      $.ui.log(`Claude refused a steer, so attn sends it when the turn ends: ${appended.deny}`, { to: 'debug' })
      steers = [...steers, message]
      continue
    }
    await ackHeld($, message, 'appended')
  }
}

function takeHeld(): Message[] {
  const held = [...interrupts, ...steers, ...queued]
  interrupts = []
  steers = []
  return held
}

function nextPrompt(messages: Message[]): { prompt: Message[]; rest: Message[] } {
  const first = messages[0]
  const asUser =
    first?.turn === 'interrupt' ? first.user_words === true : messages.some(message => message.user_words === true)
  const inVoice = (message: Message) => (message.user_words === true) === asUser
  return { prompt: messages.filter(inVoice), rest: messages.filter(message => !inVoice(message)) }
}

async function submitPrompt($: EngineInterface, prompt: Message[], interrupted: boolean): Promise<void> {
  const drop = await submit($, prompt.map(message => message.text).join('\n\n'), prompt[0]?.user_words === true)
  if (drop !== undefined) {
    const back = [...prompt, ...takeHeld()]
    await Promise.all([setQueued($, []), setTurnRunning($, false)])
    return giveBack($, back)
  }
  for (const message of prompt) {
    await ackHeld($, message, interrupted && message.turn === 'interrupt' ? 'interrupted' : 'submitted')
  }
}

async function awaitAttnTools($: EngineInterface): Promise<void> {
  try {
    const ended = await Promise.race([$.mcp.connect('attn'), $.clock.sleep(TOOLS_WAIT_MS).then(() => 'bound' as const)])
    if (ended === 'bound') {
      $.ui.log(`attn's tools had not connected after ${TOOLS_WAIT_MS} ms, so the session starts without them`, { to: 'debug' })
    } else if (!ended.isConnected && ended.reason !== 'unlisted') {
      $.ui.log(`attn's tools did not connect (${ended.reason}): ${ended.message}`, { to: 'debug' })
    }
  } catch (err) {
    $.ui.log(`waiting for attn's tools failed: ${String(err)}`, { to: 'debug' })
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    link = {
      terminal: (await $.env.get('ATTN_TERMINAL_ID')) ?? '',
      socketPath: (await $.env.get('ATTN_CLAUDE_MOD_SOCKET')) ?? '',
    }
    isTurnRunning = await read($, persistedTurnRunning)
    turnId = await read($, persistedTurnId)
    await setQueued($, [])
    await awaitAttnTools($)
    void pollForever($)
    return started
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      isStopped = false
      turnId = e.turnId
      await Promise.all([setTurnRunning($, true), update($, persistedTurnId, () => e.turnId)])
      await update($, waitingLines, () => [])
    }
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    if (e.agentId === undefined && next.signal.aborted !== true && steers.length > 0) {
      await appendSteers($)
    }
    return result
  })

  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    if (e.agentId !== undefined) {
      return result
    }
    const interrupted = e.isAborted && ownAbort
    isStopped = e.isAborted && !ownAbort
    ownAbort = false
    const held = takeHeld()
    if (isStopped) {
      await Promise.all([setQueued($, []), setTurnRunning($, false)])
      await listWaiting($, held)
      if (held.length > 0) {
        await giveBack($, held)
      }
      return result
    }
    const { prompt, rest } = nextPrompt(held)
    await Promise.all([setQueued($, rest), setTurnRunning($, prompt.length > 0)])
    if (prompt.length > 0) {
      void submitPrompt($, prompt, interrupted)
    }
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const held = await read($, heldLines)
    const waiting = await read($, waitingLines)
    if (e.props.hasSurvey || (held.length === 0 && waiting.length === 0)) {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    return (
      <Box flexDirection="column">
        {held.map((line, index) => (
          <Text key={`held-${index}`} wrap="truncate">
            ❯ {line}
          </Text>
        ))}
        {held.length > 0 && <Text dimColor>attn sends this when the turn ends</Text>}
        {waiting.length > 0 && <Text dimColor>Waiting in attn — sent with your next prompt</Text>}
        {waiting.map((line, index) => (
          <Text key={`waiting-${index}`} dimColor wrap="truncate">
            ❯ {line}
          </Text>
        ))}
      </Box>
    )
  })
}
