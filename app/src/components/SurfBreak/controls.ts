import { restingInput, type SurfInput } from './ocean';

export type HeldAction = Exclude<keyof SurfInput, 'jump' | 'posture'>;
const keys: Record<string, HeldAction> = {
  ArrowLeft: 'left', KeyA: 'left', ArrowRight: 'right', KeyD: 'right',
  ArrowUp: 'away', KeyW: 'away', ArrowDown: 'toward', KeyS: 'toward',
  ShiftLeft: 'dive', ShiftRight: 'dive', KeyQ: 'tail', KeyE: 'nose',
};
export const surfButtons: { action: HeldAction; key: string; label: string; name: string }[] = [
  { action: 'left', key: '←', label: 'left', name: 'Steer left' },
  { action: 'right', key: '→', label: 'right', name: 'Steer right' },
  { action: 'away', key: '↑', label: 'away', name: 'Steer away from the screen' },
  { action: 'toward', key: '↓', label: 'toward', name: 'Steer toward the screen' },
  { action: 'dive', key: 'shift', label: 'dive', name: 'Hold to dive, release to float up' },
  { action: 'tail', key: 'q', label: 'tail', name: 'Step toward the tail' },
  { action: 'nose', key: 'e', label: 'nose', name: 'Step toward the nose' },
];

export class SurfControls {
  input = restingInput();
  private heldKeys = new Set<string>();
  private pointers = new Map<number, HeldAction>();

  press(code: string) {
    if (!keys[code]) return false;
    this.heldKeys.add(code);
    this.sync();
    return true;
  }

  release(code: string) { this.heldKeys.delete(code); this.sync(); }
  holdPointer(id: number, action: HeldAction) { this.pointers.set(id, action); this.sync(); }
  releasePointer(id: number) { this.pointers.delete(id); this.sync(); }
  clear() { this.heldKeys.clear(); this.pointers.clear(); this.input = restingInput(); }

  private sync() {
    const { jump, posture } = this.input;
    this.input = restingInput();
    this.input.jump = jump;
    this.input.posture = posture;
    for (const code of this.heldKeys) this.input[keys[code]] = true;
    for (const action of this.pointers.values()) this.input[action] = true;
  }
}
