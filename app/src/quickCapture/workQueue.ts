// Twenty distinct 5.26 MB screenshots: 2 slots ready in 2.412s; 4/8 save only 53/88ms.
// Hosted memory/time receipt: docs/context/quick-capture.md#attachment-work-capacity.
const CAPTURE_WORK_CAPACITY = 2;

export class CaptureWorkQueue {
  protected active = 0;
  protected pending: (() => void)[] = [];
  protected capacity = CAPTURE_WORK_CAPACITY;
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
