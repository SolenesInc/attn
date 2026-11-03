import { describe, expect, it } from 'vitest'
import { afterOutput, atBottom, drag, historyRows, type ScrollFrame } from './scroll'

const shell: ScrollFrame = { rowHeight: 10, overflow: 0, history: 100, programScrolls: false }
const fullscreenAgent: ScrollFrame = { rowHeight: 10, overflow: 0, history: 0, programScrolls: true }

const swipe = (frame: ScrollFrame, downs: ReadonlyArray<number>) => {
  let position = atBottom
  let wheelLines = 0
  for (const down of downs) {
    const dragged = drag(position, frame, down)
    position = dragged.position
    wheelLines += dragged.wheelLines
  }
  return {
    position,
    seen: {
      panned: Math.min(position.fromBottom, frame.overflow),
      historyRows: historyRows(position, frame),
      wheelLines,
    },
  }
}

describe('dragging the phone terminal', () => {
  it.each([
    ['a swipe down reads rows of scrollback', shell, [35], 0, 4, 0],
    ['a swipe back up returns toward the live screen', shell, [200, -60], 0, 14, 0],
    ['scrollback ends at its oldest row', shell, [990, 50], 0, 100, 0],
    ['the live screen is the bottom', shell, [20, -50], 0, 0, 0],
    ['the screen hidden by the keyboard pans first', { ...shell, overflow: 40 }, [30], 30, 0, 0],
    ['past the hidden screen comes scrollback', { ...shell, overflow: 40 }, [30, 30], 40, 2, 0],
    ['a program that scrolls itself gets wheel-up lines', fullscreenAgent, [35], 0, 0, -3],
    ['and wheel-down lines for a swipe up', fullscreenAgent, [-25], 0, 0, 2],
    ['short moves add up to whole lines', fullscreenAgent, [6, 5, 5, 5], 0, 0, -2],
    ['a swipe that turns back cancels its partial line', fullscreenAgent, [6, -6, 6, -6], 0, 0, 0],
    ['its hidden screen pans before the wheel', { ...fullscreenAgent, overflow: 40 }, [30, 30], 40, 0, -2],
    ['and pans back before the wheel turns down', { ...fullscreenAgent, overflow: 40 }, [30, -40], 0, 0, 1],
  ])('%s', (_, frame, downs, panned, rows, wheelLines) => {
    expect(swipe(frame, downs).seen).toEqual({ panned, historyRows: rows, wheelLines })
  })
})

describe('output while the phone shows the terminal', () => {
  it.each([
    ['keeps the scrollback being read in place', shell, [300], 3, 33],
    ['follows new output at the live screen', shell, [], 3, 0],
    ['follows new output while panning the hidden screen', { ...shell, overflow: 40 }, [30], 3, 0],
  ])('%s', (_, frame, downs, grown, rows) => {
    const { position } = swipe(frame, downs)
    const after = { ...frame, history: frame.history + grown }
    expect(historyRows(afterOutput(position, after, frame.history), after)).toBe(rows)
  })
})
