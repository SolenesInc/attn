export interface SpikeCounters {
  marks: Record<string, number>
  paints: number
  paintSkips: number
  framesWritten: number
  inputLog: string[]
}

export const counters: SpikeCounters = {
  marks: {},
  paints: 0,
  paintSkips: 0,
  framesWritten: 0,
  inputLog: [],
}

;(globalThis as any).__spike = counters

// Native shells have no console to evaluate window.__spike from, so a build can log the counters on a timer.
declare const __ATTN_SPIKE_STATS_MS__: number
if (__ATTN_SPIKE_STATS_MS__ > 0) {
  setInterval(() => {
    console.log(`SPIKE_STATS t=${Math.round(performance.now())}ms paints=${counters.paints} paintSkips=${counters.paintSkips} framesWritten=${counters.framesWritten}`)
  }, __ATTN_SPIKE_STATS_MS__)
}

export function mark(name: string): void {
  if (counters.marks[name] !== undefined) return
  const t = Math.round(performance.now())
  counters.marks[name] = t
  console.log(`SPIKE_MARK ${name} t=${t}ms`)
}

export function logInput(line: string): void {
  counters.inputLog.push(line)
  if (counters.inputLog.length > 40) counters.inputLog.shift()
  console.log(`SPIKE_INPUT ${line}`)
  document.dispatchEvent(new CustomEvent('spike-input-log'))
}
