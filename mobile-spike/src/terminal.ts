import { attachTerminalInput, type GhosttyTerminal } from '@attn/ghostty'
import { loadGhostty } from '@attn/ghostty/wasm'
import { WebGlTerminalRenderer } from '@attn/components/GhosttyWebGlRenderer'
import { enableGraphemeClustering } from '@attn/components/terminalGraphemeMode'
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
  getTerminalAnsiPalette,
  getTerminalTheme,
} from '@attn/utils/terminalSizing'
import { daemon } from './daemon'
import { counters, logInput, mark } from './metrics'

export type SizeMode = 'fit' | 'native'
export type InputMode = 'direct' | 'composer'
export type SpecialKey = 'Esc' | 'Tab' | 'Up' | 'Down' | 'Left' | 'Right' | 'CtrlC' | 'Enter' | 'ShiftTab' | '1' | '2' | '3'

export interface TerminalOptions {
  sessionId: string
  sizeMode: SizeMode
  inputMode: InputMode
  fontSize: number
}

export interface TerminalHandle {
  sendKey(key: SpecialKey): void
  submit(text: string): Promise<void>
  setCtrlLatched(latched: boolean): void
  focusInput(): void
  info(): string
}

export let activeTerminal: TerminalHandle | null = null

const ARROW_KEYS: Partial<Record<SpecialKey, string>> = {
  Up: 'ARROW_UP', Down: 'ARROW_DOWN', Left: 'ARROW_LEFT', Right: 'ARROW_RIGHT',
}
const SUBMIT_GAP_MS = 150

function colorNumber(value: string): number {
  return Number.parseInt(value.slice(1), 16)
}

function ctrlChar(ch: string): string | null {
  const c = ch.toLowerCase()
  if (c.length !== 1 || c < 'a' || c > 'z') return null
  return String.fromCharCode(c.charCodeAt(0) - 96)
}

function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i)
  return out
}

export function mountTerminal(element: HTMLElement, opts: TerminalOptions, onStatus: (text: string) => void): () => void {
  let disposed = false
  const openedAt = performance.now()
  const cleanups: Array<() => void> = []
  const viewport = document.createElement('div')
  viewport.className = 'term-viewport'
  const stage = document.createElement('div')
  stage.className = 'term-stage'
  const canvas = document.createElement('canvas')
  stage.appendChild(canvas)
  viewport.appendChild(stage)
  element.appendChild(viewport)

  let input: HTMLDivElement | null = null
  if (opts.inputMode === 'direct') {
    input = document.createElement('div')
    input.className = 'term-input'
    input.contentEditable = 'true'
    input.setAttribute('role', 'textbox')
    input.setAttribute('aria-label', 'Terminal input')
    input.setAttribute('autocapitalize', 'off')
    input.setAttribute('autocorrect', 'off')
    input.spellcheck = false
    viewport.appendChild(input)
  }

  let terminal: GhosttyTerminal | null = null
  let renderer: WebGlTerminalRenderer | null = null
  let rafId: number | null = null
  let forcePaint = false
  let ctrlLatched = false
  let scale = 1
  let panX = 0
  let panY = 0
  let userZoomed = false
  let firstPaintLogged = false

  const terminalId = daemon.terminalBySession.get(opts.sessionId) ?? opts.sessionId
  const send = (data: string) => {
    logInput(`send ${JSON.stringify(data)}`)
    daemon.input(terminalId, data)
  }

  const paint = () => {
    rafId = null
    if (!terminal || !renderer) return
    const sample = renderer.render(terminal, forcePaint)
    forcePaint = false
    if (sample) {
      counters.paints += 1
      if (counters.paints === 1) mark('first_terminal_paint')
      if (!firstPaintLogged) { firstPaintLogged = true; console.log(`SPIKE_TERM open_to_first_paint=${Math.round(performance.now() - openedAt)}ms`) }
    } else counters.paintSkips += 1
  }
  const schedulePaint = (force = false) => {
    forcePaint ||= force
    if (rafId === null) rafId = requestAnimationFrame(paint)
  }

  const applyTransform = () => {
    stage.style.transform = `translate(${panX}px, ${panY}px) scale(${scale})`
  }

  const canvasCss = () => ({
    w: (terminal?.cols ?? 0) * (renderer?.cellWidth ?? 0),
    h: (terminal?.rows ?? 0) * (renderer?.cellHeight ?? 0),
  })

  const clampPan = () => {
    const { w, h } = canvasCss()
    const vw = viewport.clientWidth
    const vh = viewport.clientHeight
    const sw = w * scale
    const sh = h * scale
    panX = sw <= vw ? 0 : Math.min(0, Math.max(vw - sw, panX))
    panY = sh <= vh ? vh - sh : Math.min(0, Math.max(vh - sh, panY))
  }

  const fitToWidth = () => {
    const { w } = canvasCss()
    if (!w) return
    scale = viewport.clientWidth / w
    panX = 0
    panY = Number.NEGATIVE_INFINITY
    clampPan()
    applyTransform()
  }

  const resizeModel = (cols: number, rows: number) => {
    if (!terminal || !renderer) return
    if (cols === terminal.cols && rows === terminal.rows) return
    terminal.resize(cols, rows)
    renderer.resize(cols, rows)
    schedulePaint(true)
  }

  const fitPty = () => {
    if (!renderer || !terminal) return
    const dims = renderer.fitDimensions(viewport.clientWidth, viewport.clientHeight)
    if (dims.cols === terminal.cols && dims.rows === terminal.rows) return
    resizeModel(dims.cols, dims.rows)
    daemon.resize(terminalId, dims.cols, dims.rows)
    onStatus(`PTY ${dims.cols}x${dims.rows} (fit)`)
  }

  const onViewportResize = () => {
    if (opts.sizeMode === 'fit') fitPty()
    else if (userZoomed) { clampPan(); applyTransform() } else fitToWidth()
  }

  const ro = new ResizeObserver(() => onViewportResize())
  ro.observe(viewport)
  cleanups.push(() => ro.disconnect())

  if (opts.sizeMode === 'native') {
    const pointers = new Map<number, { x: number; y: number }>()
    let pinchStart: { dist: number; scale: number; midX: number; midY: number; panX: number; panY: number } | null = null
    const dist = () => {
      const [a, b] = [...pointers.values()]
      return Math.hypot(a.x - b.x, a.y - b.y)
    }
    const down = (e: PointerEvent) => {
      pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
      if (pointers.size === 2) {
        const [a, b] = [...pointers.values()]
        pinchStart = { dist: dist(), scale, midX: (a.x + b.x) / 2, midY: (a.y + b.y) / 2, panX, panY }
      }
    }
    const move = (e: PointerEvent) => {
      const prev = pointers.get(e.pointerId)
      if (!prev) return
      const next = { x: e.clientX, y: e.clientY }
      pointers.set(e.pointerId, next)
      if (pointers.size === 2 && pinchStart) {
        const rect = viewport.getBoundingClientRect()
        const ratio = dist() / pinchStart.dist
        const minScale = viewport.clientWidth / Math.max(1, canvasCss().w)
        scale = Math.min(3, Math.max(minScale, pinchStart.scale * ratio))
        const mx = pinchStart.midX - rect.left
        const my = pinchStart.midY - rect.top
        panX = mx - (mx - pinchStart.panX) * (scale / pinchStart.scale)
        panY = my - (my - pinchStart.panY) * (scale / pinchStart.scale)
        userZoomed = true
      } else if (pointers.size === 1) {
        panX += next.x - prev.x
        panY += next.y - prev.y
      }
      clampPan()
      applyTransform()
    }
    const up = (e: PointerEvent) => {
      pointers.delete(e.pointerId)
      if (pointers.size < 2) pinchStart = null
    }
    const dbl = () => { userZoomed = false; fitToWidth() }
    viewport.addEventListener('pointerdown', down)
    viewport.addEventListener('pointermove', move)
    viewport.addEventListener('pointerup', up)
    viewport.addEventListener('pointercancel', up)
    viewport.addEventListener('dblclick', dbl)
    cleanups.push(() => {
      viewport.removeEventListener('pointerdown', down)
      viewport.removeEventListener('pointermove', move)
      viewport.removeEventListener('pointerup', up)
      viewport.removeEventListener('pointercancel', up)
      viewport.removeEventListener('dblclick', dbl)
    })
  }

  if (input) {
    const el = input
    const diag = (e: Event) => {
      const k = e as KeyboardEvent & InputEvent & CompositionEvent
      if (e.type === 'keydown' || e.type === 'keyup') logInput(`${e.type} key=${JSON.stringify(k.key)} code=${k.code} keyCode=${k.keyCode} composing=${k.isComposing}`)
      else if (e.type === 'beforeinput' || e.type === 'input') logInput(`${e.type} type=${k.inputType} data=${JSON.stringify(k.data)} cancelable=${e.cancelable}`)
      else logInput(`${e.type} data=${JSON.stringify(k.data)}`)
    }
    const diagEvents = ['keydown', 'keyup', 'beforeinput', 'input', 'compositionstart', 'compositionupdate', 'compositionend']
    for (const t of diagEvents) el.addEventListener(t, diag, { capture: true })
    const beforeInput = (e: InputEvent) => e.preventDefault()
    el.addEventListener('beforeinput', beforeInput)
    const detachInput = attachTerminalInput({
      element: el,
      terminal: () => terminal,
      send,
      interceptKey: (event) => {
        if (!ctrlLatched || event.type !== 'keydown') return false
        const c = ctrlChar(event.key)
        if (!c) return false
        send(c)
        ctrlLatched = false
        document.dispatchEvent(new CustomEvent('spike-ctrl-unlatched'))
        return true
      },
      onError: (op, err) => logInput(`input error ${op}: ${String(err)}`),
    })
    cleanups.push(() => {
      detachInput()
      el.removeEventListener('beforeinput', beforeInput)
      for (const t of diagEvents) el.removeEventListener(t, diag, { capture: true })
    })
  }

  const unsubscribe = daemon.onEvent((e) => {
    if (e.event === 'pty_resized' && e.id === terminalId && opts.sizeMode === 'native') {
      resizeModel(e.cols, e.rows)
      onStatus(`PTY ${e.cols}x${e.rows} (desktop size)`)
      if (!userZoomed) fitToWidth()
    }
  })
  cleanups.push(unsubscribe)

  const start = async () => {
    const ghostty = await loadGhostty()
    if (disposed) return
    mark('ghostty_loaded')
    const theme = getTerminalTheme('dark')
    const pending: Array<{ seq: number; data: Uint8Array }> = []
    let snapshotSeq: number | null = null
    const attach = await daemon.attach(terminalId, (seq, data) => {
      if (snapshotSeq === null) { pending.push({ seq, data: data.slice() }); return }
      if (seq <= snapshotSeq || !terminal) return
      terminal.write(data)
      counters.framesWritten += 1
      schedulePaint()
    })
    if (disposed) return
    if (!attach.success) { onStatus(`attach failed: ${attach.error}`); return }
    const ptyCols = attach.cols ?? 80
    const ptyRows = attach.rows ?? 24
    terminal = ghostty.createTerminal(ptyCols, ptyRows, {
      scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
      fgColor: colorNumber(theme.foreground),
      bgColor: colorNumber(theme.background),
      cursorColor: colorNumber(theme.cursor),
      palette: getTerminalAnsiPalette('dark'),
    })
    enableGraphemeClustering(terminal)
    renderer = new WebGlTerminalRenderer(canvas, opts.fontSize, FONT_FAMILY, theme)
    renderer.resize(ptyCols, ptyRows)

    const snap = await daemon.screenSnapshot(terminalId)
    if (disposed) return
    mark('screen_snapshot')
    if (snap.success && snap.screen_snapshot) {
      if (snap.screen_cols && snap.screen_rows) resizeModel(snap.screen_cols, snap.screen_rows)
      terminal.write(b64ToBytes(snap.screen_snapshot))
    }
    snapshotSeq = snap.last_seq ?? 0
    for (const f of pending) if (f.seq > snapshotSeq) terminal.write(f.data)
    pending.length = 0

    if (opts.sizeMode === 'fit') fitPty()
    else { fitToWidth(); onStatus(`PTY ${terminal.cols}x${terminal.rows} (desktop size)`) }
    schedulePaint(true)
  }
  start().catch((err) => onStatus(`terminal error: ${String(err)}`))

  const handle: TerminalHandle = {
    sendKey(key) {
      if (ARROW_KEYS[key] && terminal) {
        send(terminal.encodeKey({ action: 'press', key: ARROW_KEYS[key]!, mods: 0, consumedMods: 0, composing: false, unshiftedCodepoint: 0 }))
        return
      }
      const map: Record<string, string> = { Esc: '\x1b', Tab: '\t', ShiftTab: '\x1b[Z', CtrlC: '\x03', Enter: '\r', 1: '1', 2: '2', 3: '3' }
      send(map[key])
    },
    async submit(text) {
      if (!terminal) return
      if (text.length > 0) {
        send(terminal.formatPaste(text))
        await new Promise((r) => setTimeout(r, SUBMIT_GAP_MS))
      }
      send('\r')
    },
    setCtrlLatched(latched) { ctrlLatched = latched },
    focusInput() { input?.focus() },
    info() {
      return terminal ? `${terminal.cols}x${terminal.rows} scale=${scale.toFixed(2)}` : 'loading'
    },
  }
  activeTerminal = handle

  return () => {
    disposed = true
    if (activeTerminal === handle) activeTerminal = null
    if (rafId !== null) cancelAnimationFrame(rafId)
    for (const c of cleanups) c()
    daemon.detach(terminalId)
    renderer?.dispose()
    terminal?.free()
    viewport.remove()
  }
}
