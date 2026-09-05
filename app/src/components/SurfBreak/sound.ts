const chords = [[48, 55, 59, 64, 67], [45, 52, 55, 59, 64], [41, 48, 52, 57, 60], [43, 50, 55, 57, 62]];
const frequency = (midi: number) => 440 * 2 ** ((midi - 69) / 12);

export class SurfSound {
  readonly context = new AudioContext();
  private master = this.context.createGain();
  private wash = this.context.createGain();
  private filter = this.context.createBiquadFilter();
  private timer: number | undefined;
  private nextChord = 0;
  private chord = 0;
  private disposed = false;
  private wantsPlayback = false;

  constructor() {
    const ctx = this.context;
    this.master.gain.value = 0;
    this.master.connect(ctx.destination);
    const buffer = ctx.createBuffer(1, ctx.sampleRate * 4, ctx.sampleRate);
    const samples = buffer.getChannelData(0);
    let brown = 0;
    for (let i = 0; i < samples.length; i++) {
      brown = (brown + (Math.random() * 2 - 1) * 0.02) / 1.02;
      samples[i] = brown * 3.5;
    }
    const noise = ctx.createBufferSource();
    noise.buffer = buffer; noise.loop = true;
    this.filter.type = 'lowpass'; this.filter.frequency.value = 600;
    this.wash.gain.value = 0.2;
    noise.connect(this.filter).connect(this.wash).connect(this.master);
    noise.start();
  }

  async play() {
    if (this.disposed) return;
    this.wantsPlayback = true;
    await this.context.resume();
    if (this.disposed || !this.wantsPlayback) return;
    this.master.gain.setTargetAtTime(0.65, this.context.currentTime, 0.4);
    if (this.timer !== undefined) return;
    if (!this.nextChord) this.nextChord = this.context.currentTime + 0.08;
    this.schedule();
    this.timer = window.setInterval(() => this.schedule(), 1000);
  }

  async pause() {
    this.wantsPlayback = false;
    if (this.timer !== undefined) window.clearInterval(this.timer);
    this.timer = undefined;
    if (!this.disposed) await this.context.suspend();
  }

  private schedule() {
    const ctx = this.context;
    if (this.nextChord > ctx.currentTime + 1.5) return;
    const at = Math.max(this.nextChord, ctx.currentTime + 0.05);
    const notes = chords[this.chord++ % chords.length];
    for (const [index, midi] of notes.entries()) {
      this.note(midi, at + index * 0.045, 10, 0.027, 'sine');
    }
    for (let i = 0; i < 4; i++) {
      this.note(notes[(i + this.chord) % notes.length] + 12, at + i * 2 + 0.5, 2.7, 0.016, 'triangle');
    }
    this.nextChord = at + 8;
  }

  private note(midi: number, at: number, duration: number, volume: number, type: OscillatorType) {
    const ctx = this.context;
    const oscillator = ctx.createOscillator();
    const envelope = ctx.createGain();
    oscillator.type = type; oscillator.frequency.value = frequency(midi);
    envelope.gain.setValueAtTime(0, at);
    envelope.gain.linearRampToValueAtTime(volume, at + Math.min(1.6, duration * 0.2));
    envelope.gain.exponentialRampToValueAtTime(0.0001, at + duration);
    oscillator.connect(envelope).connect(this.master);
    oscillator.start(at); oscillator.stop(at + duration + 0.1);
    oscillator.onended = () => { oscillator.disconnect(); envelope.disconnect(); };
  }

  setSurf(speed: number, time: number) {
    if (this.disposed || this.context.state !== 'running') return;
    const now = this.context.currentTime;
    this.wash.gain.setTargetAtTime(0.16 + speed * 0.13 + Math.sin(time * 0.6) * 0.025, now, 0.2);
    this.filter.frequency.setTargetAtTime(450 + speed * 800, now, 0.2);
  }

  dispose() {
    this.disposed = true;
    if (this.timer !== undefined) window.clearInterval(this.timer);
    this.timer = undefined;
    return this.context.close();
  }
}
