// Twenty distinct 5.26 MB screenshots: 2 slots ready in 2.412s; 4/8 save only 53/88ms.
// Hosted memory/time receipt: docs/context/quick-capture.md#attachment-work-capacity.
const CAPTURE_WORK_CAPACITY = 2;

export class CaptureWorkQueue {
  private active = 0;
  private pending: (() => void)[] = [];
  private peak = 0;
  private idle: (() => void)[] = [];
  private limit = CAPTURE_WORK_CAPACITY;
  setCapacity(capacity: number) {
    if (this.active || this.pending.length) throw new Error('Capture work is still running.');
    this.limit = capacity;
    this.peak = 0;
  }
  whenIdle(): Promise<void> {
    if (!this.active && !this.pending.length) return Promise.resolve();
    return new Promise(resolve => this.idle.push(resolve));
  }
  snapshot() { return { active: this.active, pending: this.pending.length, peak: this.peak, capacity: this.limit }; }
  run<T>(work: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      this.pending.push(() => {
        this.active++;
        this.peak = Math.max(this.peak, this.active);
        void work().then(resolve, reject).finally(() => { this.active--; this.drain(); if (!this.active && !this.pending.length) this.idle.splice(0).forEach(resolve => resolve()); });
      });
      this.drain();
    });
  }
  private drain() {
    while (this.active < this.limit && this.pending.length) this.pending.shift()!();
  }
}
