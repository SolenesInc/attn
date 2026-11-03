import { WebGlTerminalRenderer } from '@attn/components/GhosttyWebGlRenderer'
import { enableGraphemeClustering } from '@attn/components/terminalGraphemeMode'
import type { GhosttyTerminal } from '@attn/ghostty'
import { loadGhostty } from '@attn/ghostty/wasm'
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
  getTerminalAnsiPalette,
  getTerminalTheme,
} from '@attn/utils/terminalSizing'
import { applicationWheelInput, scrolledViewportCells } from '@attn/utils/ghosttyScroll'
import { terminalCreditBytes, type Link, type OpenTerminal, type TerminalRequest } from '../link'
import {
  afterOutput,
  atBottom,
  drag,
  historyRows,
  settle,
  type ScrollFrame,
  type ScrollPosition,
} from './scroll'

export const terminalKeys = ['Esc', 'Up', 'Down', 'Enter', '1', '2', '3', 'Tab', 'CtrlC', 'ShiftTab'] as const
export type TerminalKey = (typeof terminalKeys)[number]

export type MountedTerminal = { readonly sendKey: (key: TerminalKey) => boolean }

export let activeTerminal: MountedTerminal | null = null

const fontSize = 13
const returnCreditAt = terminalCreditBytes / 4

const keyBytes: Record<Exclude<TerminalKey, 'Up' | 'Down'>, string> = {
  Esc: '\x1b',
  Enter: '\r',
  '1': '1',
  '2': '2',
  '3': '3',
  Tab: '\t',
  CtrlC: '\x03',
  ShiftTab: '\x1b[Z',
}

const colorNumber = (value: string) => Number.parseInt(value.slice(1), 16)

export const mountTerminal = (
  element: HTMLElement,
  link: Link,
  terminalId: string,
  onStatus: (text: string) => void,
  onTap: () => void,
): (() => void) => {
  let disposed = false
  const viewport = document.createElement('div')
  viewport.className = 'term-viewport'
  const stage = document.createElement('div')
  stage.className = 'term-stage'
  const canvas = document.createElement('canvas')
  stage.appendChild(canvas)
  viewport.appendChild(stage)
  element.appendChild(viewport)

  const theme = getTerminalTheme('dark')
  viewport.style.background = theme.background
  let terminal: GhosttyTerminal | null = null
  let renderer: WebGlTerminalRenderer | null = null
  let open: OpenTerminal | null = null
  let rafId: number | null = null
  let forcePaint = false
  let owed = 0
  let scale = 1
  let panX = 0
  let position: ScrollPosition = atBottom
  let shownHistory = 0
  let requested: TerminalRequest = { cols: 0, rows: 0 }
  let stableWidth = 0
  let stableHeight = 0

  const paint = () => {
    rafId = null
    if (!terminal || !renderer) return
    place()
    renderer.render(terminal, forcePaint, scrolledViewportCells(terminal, shownHistory), null, shownHistory)
    forcePaint = false
    if (owed >= returnCreditAt) {
      open?.grant(owed)
      owed = 0
    }
  }
  const schedulePaint = (force = false) => {
    forcePaint ||= force
    if (rafId === null) rafId = requestAnimationFrame(paint)
  }

  const canvasSize = () => ({
    width: (terminal?.cols ?? 0) * (renderer?.cellWidth ?? 0),
    height: (terminal?.rows ?? 0) * (renderer?.cellHeight ?? 0),
  })
  const scrollFrame = (): ScrollFrame | null => {
    const { width, height } = canvasSize()
    if (!terminal || !renderer || !width) return null
    scale = viewport.clientWidth / width
    return {
      rowHeight: renderer.cellHeight,
      overflow: Math.max(0, height - viewport.clientHeight / scale),
      history: terminal.getScrollbackLength(),
      programScrolls: terminal.hasMouseTracking() || terminal.isAlternateScreen(),
    }
  }
  const place = () => {
    const frame = scrollFrame()
    if (!frame) return
    const { width, height } = canvasSize()
    position = settle(position, frame)
    const shownWidth = width * scale
    panX =
      shownWidth <= viewport.clientWidth ? 0 : Math.min(0, Math.max(viewport.clientWidth - shownWidth, panX))
    const gap = viewport.clientHeight - height * scale
    const panY = Math.min(gap + position.fromBottom * scale, Math.max(0, gap))
    stage.style.transform = `translate(${panX}px, ${panY}px) scale(${scale})`
    const rows = historyRows(position, frame)
    if (rows !== shownHistory) {
      shownHistory = rows
      schedulePaint(true)
    }
  }
  const anchorBottom = () => {
    position = atBottom
    place()
  }

  const resizeModel = (cols: number, rows: number) => {
    if (!terminal || !renderer || (cols === terminal.cols && rows === terminal.rows)) return
    terminal.resize(cols, rows)
    renderer.resize(cols, rows)
    anchorBottom()
    schedulePaint(true)
  }

  const fitRequest = (): TerminalRequest | null => {
    if (!renderer || viewport.clientWidth === 0) return null
    if (viewport.clientWidth !== stableWidth) {
      stableWidth = viewport.clientWidth
      stableHeight = 0
    }
    stableHeight = Math.max(stableHeight, viewport.clientHeight)
    return renderer.fitDimensions(stableWidth, stableHeight)
  }

  const send = (data: string) => open?.send({ cmd: 'terminal_input', terminal_id: terminalId, data }) ?? false

  const start = async () => {
    const ghostty = await loadGhostty()
    if (disposed) return
    renderer = new WebGlTerminalRenderer(canvas, fontSize, FONT_FAMILY, theme)
    const fitted = fitRequest()
    if (!fitted) return onStatus('the terminal has no room to draw')
    requested = fitted
    const config = {
      scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
      fgColor: colorNumber(theme.foreground),
      bgColor: colorNumber(theme.background),
      cursorColor: colorNumber(theme.cursor),
      palette: getTerminalAnsiPalette('dark'),
    }
    onStatus('opening…')
    open = link.openTerminal(terminalId, requested, {
      screen: (screen, ansi) => {
        terminal?.free()
        terminal = ghostty.createTerminal(screen.cols, screen.rows, config)
        enableGraphemeClustering(terminal)
        renderer?.resize(screen.cols, screen.rows)
        terminal.write(ansi)
        owed += ansi.length
        anchorBottom()
        schedulePaint(true)
        onStatus('')
      },
      output: (data) => {
        if (!terminal) return
        const historyBefore = terminal.getScrollbackLength()
        terminal.write(data)
        owed += data.length
        const frame = position.fromBottom === 0 ? null : scrollFrame()
        if (frame) position = afterOutput(position, frame, historyBefore)
        schedulePaint()
      },
      resized: resizeModel,
      ended: () => onStatus('the session’s terminal ended'),
      failed: onStatus,
    })
  }
  start().catch((error) => onStatus(`the terminal failed to start: ${String(error)}`))

  const observer = new ResizeObserver(() => {
    place()
    const fitted = fitRequest()
    if (fitted && open && (fitted.cols !== requested.cols || fitted.rows !== requested.rows)) {
      requested = fitted
      open.resize(fitted)
    }
  })
  observer.observe(viewport)

  let dragFrom: { x: number; y: number } | null = null
  const down = (event: PointerEvent) => {
    dragFrom = { x: event.clientX, y: event.clientY }
  }
  const wheelAt = (event: PointerEvent, lines: number) => {
    if (!terminal) return
    const box = canvas.getBoundingClientRect()
    const cell = (offset: number, size: number, count: number) =>
      Math.min(count, Math.max(1, Math.floor((offset / size) * count) + 1))
    send(
      applicationWheelInput(
        lines,
        cell(event.clientX - box.left, box.width, terminal.cols),
        cell(event.clientY - box.top, box.height, terminal.rows),
        terminal.hasMouseTracking(),
        terminal.getMode(1006),
      ),
    )
  }
  const move = (event: PointerEvent) => {
    if (!dragFrom) return
    const frame = scrollFrame()
    panX += event.clientX - dragFrom.x
    const down = (event.clientY - dragFrom.y) / scale
    dragFrom = { x: event.clientX, y: event.clientY }
    if (frame) {
      const dragged = drag(position, frame, down)
      position = dragged.position
      if (dragged.wheelLines !== 0) wheelAt(event, dragged.wheelLines)
    }
    place()
  }
  const up = () => {
    dragFrom = null
  }
  viewport.addEventListener('pointerdown', down)
  viewport.addEventListener('pointermove', move)
  viewport.addEventListener('pointerup', up)
  viewport.addEventListener('pointercancel', up)
  viewport.addEventListener('click', onTap)
  viewport.addEventListener('dblclick', anchorBottom)

  const handle: MountedTerminal = {
    sendKey: (key) => {
      if (key === 'Up' || key === 'Down') {
        if (!terminal) return false
        const arrow = key === 'Up' ? 'ARROW_UP' : 'ARROW_DOWN'
        return send(
          terminal.encodeKey({
            action: 'press',
            key: arrow,
            mods: 0,
            consumedMods: 0,
            composing: false,
            unshiftedCodepoint: 0,
          }),
        )
      }
      return send(keyBytes[key])
    },
  }
  activeTerminal = handle

  return () => {
    disposed = true
    if (activeTerminal === handle) activeTerminal = null
    if (rafId !== null) cancelAnimationFrame(rafId)
    observer.disconnect()
    open?.close()
    renderer?.dispose()
    terminal?.free()
    viewport.remove()
  }
}
