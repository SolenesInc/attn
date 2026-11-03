export type ScrollFrame = {
  readonly rowHeight: number
  readonly overflow: number
  readonly history: number
  readonly programScrolls: boolean
}

export type ScrollPosition = {
  readonly fromBottom: number
  readonly wheelRows: number
}

export const atBottom: ScrollPosition = { fromBottom: 0, wheelRows: 0 }

const furthest = (frame: ScrollFrame) =>
  frame.programScrolls ? frame.overflow : frame.overflow + frame.history * frame.rowHeight

const clamp = (value: number, max: number) => Math.min(max, Math.max(0, value))

export const settle = (position: ScrollPosition, frame: ScrollFrame): ScrollPosition => {
  const fromBottom = clamp(position.fromBottom, furthest(frame))
  return fromBottom === position.fromBottom ? position : { ...position, fromBottom }
}

export const historyRows = (position: ScrollPosition, frame: ScrollFrame): number =>
  frame.programScrolls
    ? 0
    : Math.min(frame.history, Math.round(Math.max(0, position.fromBottom - frame.overflow) / frame.rowHeight))

export const drag = (
  position: ScrollPosition,
  frame: ScrollFrame,
  down: number,
): { readonly position: ScrollPosition; readonly wheelLines: number } => {
  const wanted = position.fromBottom + down
  const fromBottom = clamp(wanted, furthest(frame))
  if (!frame.programScrolls) return { position: { fromBottom, wheelRows: 0 }, wheelLines: 0 }
  const rows = position.wheelRows + (wanted - fromBottom) / frame.rowHeight
  const up = Math.trunc(rows)
  return { position: { fromBottom, wheelRows: rows - up }, wheelLines: -up }
}

export const afterOutput = (
  position: ScrollPosition,
  frame: ScrollFrame,
  historyBefore: number,
): ScrollPosition =>
  position.fromBottom <= frame.overflow
    ? position
    : settle(
        { ...position, fromBottom: position.fromBottom + (frame.history - historyBefore) * frame.rowHeight },
        frame,
      )
