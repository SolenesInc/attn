const QUICK_CAPTURE_WORK_CAPACITY = 2;

export class QuickCaptureWorkQueue {
  protected active = 0;
  protected pending: (() => void)[] = [];
  protected capacity = QUICK_CAPTURE_WORK_CAPACITY;
  run<T>(work: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      this.pending.push(() => {
        this.active++;
        void work().then(resolve, reject).finally(() => { this.active--; this.drain(); });
      });
      this.drain();
    });
  }
  protected drain() {
    while (this.active < this.capacity && this.pending.length) this.pending.shift()!();
  }
}
