const maxScale = 8
const doubleTapScale = 2.5
const doubleTapMs = 300

type Point = { x: number; y: number }

export const attachZoom = (element: Element): (() => void) => {
  const surface = element as HTMLElement
  const content = surface.firstElementChild as HTMLElement | null
  if (!content) return () => {}
  let scale = 1
  let at: Point = { x: 0, y: 0 }
  const pointers = new Map<number, Point>()
  let gesture: { scale: number; at: Point; distance: number; middle: Point } | undefined
  let lastTap = 0

  const apply = () => {
    content.style.transform = `translate(${at.x}px, ${at.y}px) scale(${scale})`
  }
  const middleOf = (a: Point, b: Point): Point => ({ x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 })
  const distanceOf = (a: Point, b: Point) => Math.hypot(a.x - b.x, a.y - b.y)
  const start = () => {
    const [a, b] = [...pointers.values()]
    gesture =
      a && b
        ? { scale, at: { ...at }, distance: distanceOf(a, b), middle: middleOf(a, b) }
        : a
          ? { scale, at: { ...at }, distance: 0, middle: a }
          : undefined
  }
  const zoomAround = (point: Point, from: { scale: number; at: Point }, to: number) => {
    const rect = surface.getBoundingClientRect()
    const origin = { x: point.x - rect.left - content.offsetLeft, y: point.y - rect.top - content.offsetTop }
    scale = Math.min(maxScale, Math.max(1, to))
    const ratio = scale / from.scale
    at = { x: origin.x - (origin.x - from.at.x) * ratio, y: origin.y - (origin.y - from.at.y) * ratio }
    if (scale === 1) at = { x: 0, y: 0 }
  }

  const down = (event: PointerEvent) => {
    surface.setPointerCapture(event.pointerId)
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
    start()
  }
  const move = (event: PointerEvent) => {
    if (!pointers.has(event.pointerId) || !gesture) return
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
    const [a, b] = [...pointers.values()]
    if (a && b) {
      const middle = middleOf(a, b)
      zoomAround(gesture.middle, gesture, gesture.scale * (distanceOf(a, b) / gesture.distance))
      at = { x: at.x + middle.x - gesture.middle.x, y: at.y + middle.y - gesture.middle.y }
    } else if (a && scale > 1) {
      at = { x: gesture.at.x + a.x - gesture.middle.x, y: gesture.at.y + a.y - gesture.middle.y }
    }
    apply()
  }
  const up = (event: PointerEvent) => {
    const point = pointers.get(event.pointerId)
    pointers.delete(event.pointerId)
    if (point && pointers.size === 0 && gesture && distanceOf(point, gesture.middle) < 10) {
      const now = event.timeStamp
      if (now - lastTap < doubleTapMs) {
        zoomAround(point, { scale, at }, scale > 1 ? 1 : doubleTapScale)
        apply()
        lastTap = 0
      } else {
        lastTap = now
      }
    }
    start()
  }

  surface.addEventListener('pointerdown', down)
  surface.addEventListener('pointermove', move)
  surface.addEventListener('pointerup', up)
  surface.addEventListener('pointercancel', up)
  return () => {
    surface.removeEventListener('pointerdown', down)
    surface.removeEventListener('pointermove', move)
    surface.removeEventListener('pointerup', up)
    surface.removeEventListener('pointercancel', up)
  }
}
