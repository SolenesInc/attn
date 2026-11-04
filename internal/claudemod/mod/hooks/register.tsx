import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Message } from '../types'

type Link = { terminal: string; socketPath: string }
type Outcome = 'submitted' | 'held' | 'appended' | 'stopped' | 'refused'

const POLL_RETRY_MS = 1000
const RING_ID_PREFIX = 'inbox-ring/'
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
let lastRequest: Promise<void> = Promise.resolve()

function firstLine(text: string): string {
  return text.split('\n', 1)[0] ?? ''
}

function post($: EngineInterface, path: string, body: object): Promise<void> {
  lastRequest = lastRequest.then(() => send($, path, body))
  return lastRequest
}

async function send($: EngineInterface, path: string, body: object): Promise<void> {
  try {
    await $.http.fetch(`http://attn${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ terminal: link.terminal, instance, ...body }),
      socketPath: link.socketPath,
    })
  } catch (err) {
    $.ui.log(`POST ${path} failed: ${String(err)}`, { to: 'debug' })
  }
}

function ack($: EngineInterface, id: string, outcome: Outcome, reason?: string): Promise<void> {
  return post($, '/claude/ack', { id, outcome, reason })
}

function giveBack($: EngineInterface, messages: Message[]): Promise<void> {
  return post($, '/claude/return', { ids: messages.map(message => message.id) })
}

function setTurnRunning($: EngineInterface, value: boolean): Promise<unknown> {
  isTurnRunning = value
  return update($, persistedTurnRunning, () => value)
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
  await ack($, message.id, 'stopped')
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

async function pollForever($: EngineInterface): Promise<void> {
  const query = `terminal=${encodeURIComponent(link.terminal)}&instance=${encodeURIComponent(instance)}`
  for (;;) {
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
      await place($, JSON.parse(body) as Message)
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
  for (const { id, text } of messages) {
    const appended = await $.session
      .append({ message: { type: 'user', content: [{ type: 'text', text: MID_TURN_HEAD + text + MID_TURN_TAIL }] } })
      .catch((err: unknown) => ({ deny: String(err) }))
    if (appended.deny !== undefined) {
      $.ui.log(`attn takes this back, Claude refused it (${appended.deny}): ${text}`, { to: 'debug' })
      await giveBack($, [{ id, text }])
      continue
    }
    $.ui.log(`❯ ${text}`)
    await ack($, id, 'appended')
  }
}

async function submitLeftOver($: EngineInterface, messages: Message[]): Promise<void> {
  const drop = await submit($, messages.map(message => message.text).join('\n\n'))
  if (drop !== undefined) {
    const back = [...messages, ...held]
    await Promise.all([setHeld($, []), setTurnRunning($, false)])
    return giveBack($, back)
  }
  for (const { id } of messages) {
    await ack($, id, 'submitted')
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
    await Promise.all([setHeld($, []), setTurnRunning($, !e.isAborted && leftOver.length > 0)])
    if (e.isAborted) {
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
