import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Message, Request } from '../types'

type Link = { terminal: string; socketPath: string }
type Outcome = 'submitted' | 'held' | 'appended' | 'refused'
type Compacted = 'compacted' | 'skipped' | 'busy' | 'ready' | 'failed'

const POLL_RETRY_MS = 1000
const RING_ID_PREFIX = 'inbox-ring/'
const BETWEEN_TURNS_ONLY = ['compacts between turns', 'a newer turn began']
const MID_TURN_HEAD = 'The user sent a new message while you were working:\n'
const MID_TURN_TAIL =
  '\n\nThis is how Claude Code surfaces messages the user sends mid-turn — within the running turn, often alongside the next tool result, rather than as a separate conversation turn. Address the message above as you continue this turn.'

const persistedTurnRunning = atom({ plugin: 'attn', key: 'isTurnRunning' } as const, false)
const heldLines = atom({ plugin: 'attn', key: 'heldLines' } as const, [] as string[])
const waitingLines = atom({ plugin: 'attn', key: 'waitingLines' } as const, [] as string[])

const instance = crypto.randomUUID()
let link: Link = { terminal: '', socketPath: '' }
let isTurnRunning = false
let isStopped = false
let held: Message[] = []
let givenBack: Message[] = []
let lastRequest: Promise<void> = Promise.resolve()
let turnsEnded = 0
let compactRefused = false

function firstLine(text: string): string {
  return text.split('\n', 1)[0] ?? ''
}

function oneRow(text: string): string {
  return text.replace(/\s*\n\s*/g, ' ')
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

function setHeld($: EngineInterface, messages: Message[]): Promise<unknown> {
  held = messages
  return update($, heldLines, () => messages.map(message => firstLine(message.text)))
}

function listWaiting($: EngineInterface, messages: Message[]): Promise<unknown> {
  const rings = messages.filter(message => message.id.startsWith(RING_ID_PREFIX))
  return update($, waitingLines, lines => [...new Set([...lines, ...rings.map(ring => firstLine(ring.text))])])
}

async function submit($: EngineInterface, text: string): Promise<string | undefined> {
  try {
    const entered = await $.prompt.submit({ text, asUser: true })
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
  if (isTurnRunning) {
    await Promise.all([setHeld($, [...held, message]), ack($, message.id, 'held')])
    return
  }
  const drop = await submit($, message.text)
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

async function appendHeld($: EngineInterface): Promise<void> {
  const messages = held
  if (messages.length === 0) {
    return
  }
  await setHeld($, [])
  for (const message of messages) {
    const { text } = message
    const appended = await $.session
      .append({ message: { type: 'user', content: [{ type: 'text', text: MID_TURN_HEAD + text + MID_TURN_TAIL }] } })
      .catch((err: unknown) => ({ deny: String(err) }))
    if (appended.deny !== undefined) {
      $.ui.log(`attn takes this back, Claude refused it (${appended.deny}): ${oneRow(text)}`, { to: 'debug' })
      await giveBack($, [message])
      continue
    }
    $.ui.log(`❯ ${oneRow(text)}`)
    await ackHeld($, message, 'appended')
  }
}

async function submitLeftOver($: EngineInterface, messages: Message[]): Promise<void> {
  const drop = await submit($, messages.map(message => message.text).join('\n\n'))
  if (drop !== undefined) {
    const back = [...messages, ...held]
    await Promise.all([setHeld($, []), setTurnRunning($, false)])
    return giveBack($, back)
  }
  for (const message of messages) {
    await ackHeld($, message, 'submitted')
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
    await setHeld($, [])
    void pollForever($)
    return started
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      isStopped = false
      await setTurnRunning($, true)
      await update($, waitingLines, () => [])
    }
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    if (e.agentId === undefined && next.signal.aborted !== true) {
      await appendHeld($)
    }
    return result
  })

  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    if (e.agentId !== undefined) {
      return result
    }
    const leftOver = held
    isStopped = e.isAborted
    await Promise.all([setHeld($, []), setTurnRunning($, !isStopped && leftOver.length > 0)])
    if (isStopped) {
      await listWaiting($, leftOver)
      if (leftOver.length > 0) {
        await giveBack($, leftOver)
      }
    } else if (leftOver.length > 0) {
      void submitLeftOver($, leftOver)
    }
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const steers = await read($, heldLines)
    const waiting = await read($, waitingLines)
    if (e.props.hasSurvey || (steers.length === 0 && waiting.length === 0)) {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    return (
      <Box flexDirection="column">
        {steers.map((line, index) => (
          <Text key={`steer-${index}`} wrap="truncate">
            ❯ {line}
          </Text>
        ))}
        {steers.length > 0 && <Text dimColor>attn adds this after the current tool call</Text>}
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
