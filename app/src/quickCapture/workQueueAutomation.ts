import { QuickCaptureWorkQueue } from './workQueue';

export class QuickCaptureAutomationWorkQueue extends QuickCaptureWorkQueue {
  private peak = 0;
  private idle: (() => void)[] = [];
  setCapacity(capacity: number) {
    if (this.active || this.pending.length) throw new Error('Capture work is still running.');
    this.capacity = capacity;
    this.peak = 0;
  }
  whenIdle(): Promise<void> {
    if (!this.active && !this.pending.length) return Promise.resolve();
    return new Promise(resolve => this.idle.push(resolve));
  }
  snapshot() { return { active: this.active, pending: this.pending.length, peak: this.peak, capacity: this.capacity }; }
  override run<T>(work: () => Promise<T>): Promise<T> {
    return super.run(() => { this.peak = Math.max(this.peak, this.active); return work(); });
  }
  protected override drain() {
    super.drain();
    if (!this.active && !this.pending.length) this.idle.splice(0).forEach(resolve => resolve());
  }
}
