import { drawOcean } from './paintOcean';
import { beaches, bottomDepth, defaultConditions, setLull, type BeachId, type SurfConditions } from './beaches';
import { barrelRoof, clamp, createWave, curlPoint, ease, evolveWave, GRAVITY, noise, OCEAN_WIDTH, project, sampleWater, SEA_LEVEL, seaFloor, waterSurface, WAVE_PACE, waveSection, type Wave } from './water';
export { clamp, createWave, curlPoint, evolveWave, noise, OCEAN_HEIGHT, OCEAN_WIDTH, project, SEA_LEVEL, seaFloor, waveLift, waveSection } from './water';
export type { Wave, WaveShape } from './water';

export type SurfInput = {
  left: boolean; right: boolean; away: boolean; toward: boolean;
  dive: boolean; jump: boolean; posture: boolean; tail: boolean; nose: boolean;
};
export const restingInput = (): SurfInput => ({
  left: false, right: false, away: false, toward: false, dive: false, jump: false, posture: false, tail: false, nose: false,
});
export type WaterParticle = { kind: 'spray' | 'foam' | 'bubble'; x: number; y: number; z: number; vx: number; vy: number; life: number; size: number };
// Depth is compressed on screen; carving conserves speed in board-space units.
const DEPTH_SPEED_SCALE = 3.5;
const STAND_SPEED = 28;
const SETTLE_SPEED = 14;

export class Ocean {
  readonly beach;
  readonly conditions;
  readonly camera = { x: 0, y: 0 };
  onFoot = false;
  recovery = 0;
  wipeoutCause: 'lip' | 'stall' | 'closeout' | null = null;
  time = 0;
  x = OCEAN_WIDTH * 0.6375;
  y = SEA_LEVEL;
  z = 30;
  vx = 0;
  vy = 0;
  vz = 0;
  angle = 0;
  heading = Math.PI;
  stance = 0;
  walking = false;
  armsCrossed = false;
  posture: 'prone' | 'standing' = 'prone';
  standingBlend = 0;
  paddling = false;
  speed = 0;
  readonly waves: Wave[] = [];
  readonly particles: WaterParticle[] = [];
  private nextWave = 3;
  private waveNumber = 0;
  private setNumber = 0;
  private wavesInSet = 0;
  private jumpCooldown = 0;
  private sprayDebt = 0;
  private trailDebt = 0;
  private particleNumber = 0;
  private jumping = false;
  private slowTime = 0;
  private unstableTime = 0;

  constructor(options: { beach?: BeachId; conditions?: SurfConditions; start?: 'beach' | 'water' } = {}) {
    this.beach = beaches[options.beach ?? 'sandbar'];
    this.conditions = { ...defaultConditions(), ...options.conditions };
    this.onFoot = options.start === 'beach';
    if (this.onFoot) this.x = -80;
    this.y = this.onFoot ? this.floor(this.x, this.z) : this.surface(this.x, this.z);
    this.camera.x = this.x - OCEAN_WIDTH * 0.46;
  }

  get boardSpeed() { return Math.hypot(this.vx, this.vz * DEPTH_SPEED_SCALE); }
  get canStand() { return !this.onFoot && !this.recovery && this.boardSpeed >= STAND_SPEED && Math.abs(this.y - this.surface(this.x, this.z)) < 8; }
  floor(x: number, z = 0) { return seaFloor(x, z, this.beach); }
  project(x: number, y: number, z = 0) { return project(x, y, z, this.camera); }
  flow(x: number, z = 0, y = SEA_LEVEL) { return sampleWater(this.waves, this.beach, x, z, this.time, y); }

  surface(x: number, z = 0) { return waterSurface(this.waves, x, z, this.time); }

  get depth() { return Math.max(0, this.y - this.surface(this.x, this.z)); }
  get barrel() {
    return this.waves.find(wave => {
      const section = waveSection(wave, this.z);
      if (section.curl < 0.45 || section.collapse > 0.8) return false;
      const roof = barrelRoof(wave, this.x, this.z);
      return this.y > roof + 8 && this.y < this.surface(this.x, this.z) + 10;
    });
  }
  get cover() { return this.barrel ? ease(this.z / 65) : 0; }
  get state(): 'walking' | 'wading' | 'recovering' | 'submerged' | 'airborne' | 'riding' | 'floating' {
    if (this.onFoot) return this.floor(this.x, this.z) > this.surface(this.x, this.z) + 2 ? 'wading' : 'walking';
    if (this.recovery > 0) return 'recovering';
    const offset = this.y - this.surface(this.x, this.z);
    if (offset > 9) return 'submerged';
    if (offset < -7) return 'airborne';
    return (this.barrel || SEA_LEVEL - this.surface(this.x, this.z) > 5) && Math.hypot(this.vx, this.vz) > 8 ? 'riding' : 'floating';
  }

  step(dt: number, input: SurfInput) {
    if (!Number.isFinite(dt) || dt <= 0) return;
    const elapsed = Math.min(dt, 0.1);
    const steps = Math.ceil(elapsed * 120);
    for (let i = 0; i < steps; i++) this.advance(elapsed / steps, input, i === 0);
  }

  private advance(dt: number, input: SurfInput, first: boolean) {
    const previousSurface = this.surface(this.x, this.z);
    const supported = !this.jumping && this.y >= previousSurface - 4 && this.y <= previousSurface + 8;
    this.time += dt;
    this.jumpCooldown = Math.max(0, this.jumpCooldown - dt);
    this.recovery = Math.max(0, this.recovery - dt);
    if (!this.recovery) this.wipeoutCause = null;
    this.advanceWaves(dt);
    if (this.onFoot) {
      this.advanceOnFoot(dt, input);
      this.followCamera(dt);
      this.advanceParticles(dt, false, false);
      return;
    }
    const water = this.surface(this.x, this.z);
    const flow = this.flow(this.x, this.z, this.y);
    const slope = flow.slopeX;
    const depthSlope = flow.slopeZ;
    const waterVelocity = clamp((water - previousSurface) / dt, -160, 160);
    const touching = this.y >= water - 3 && (!this.jumping || this.vy >= waterVelocity);
    const submerged = this.y > water + 9;
    const steer = Number(input.right) - Number(input.left);
    const depthSteer = Number(input.away) - Number(input.toward);
    const lift = clamp((SEA_LEVEL - water) / 65, 0, 1);
    if (input.posture && first) {
      if (this.posture === 'standing') this.posture = 'prone';
      else if (this.canStand && !input.dive) this.posture = 'standing';
      this.slowTime = 0;
    }
    if (input.dive || submerged || this.recovery) this.posture = 'prone';
    const standing = this.posture === 'standing';

    if (input.dive || this.recovery) {
      this.jumping = false;
      const target = Math.min(this.floor(this.x, this.z) - 8, Math.max(SEA_LEVEL + (this.recovery ? 20 : 38), water + (this.recovery ? 18 : 38)));
      const velocity = clamp((target - this.y) * 1.4, -18, 18);
      this.vy += (velocity - this.vy) * (1 - Math.exp(-dt * 4));
    } else if (touching) {
      this.jumping = false;
      const buoyancy = standing ? 4 : 3;
      const limit = standing ? 32 : 28;
      const velocity = clamp((water - this.y) * buoyancy + waterVelocity + slope * this.vx + depthSlope * this.vz, -limit, limit);
      this.vy += (velocity - this.vy) * (1 - Math.exp(-dt * (standing ? 6 : 5)));
    } else {
      this.vy += GRAVITY * dt;
    }

    const inWater = touching || submerged || input.dive;
    this.paddling = !standing && inWater && Boolean(steer || depthSteer);
    if (standing || !inWater) {
      let speed = this.boardSpeed * Math.exp(-dt * (inWater ? 0.32 : 0.06));
      let direction = Math.atan2(this.vz * DEPTH_SPEED_SCALE, this.vx);
      if (steer || depthSteer) {
        const target = Math.atan2(depthSteer, steer);
        const turn = Math.atan2(Math.sin(target - direction), Math.cos(target - direction));
        const rotation = clamp(turn, -dt * (inWater ? 7 : 1.1), dt * (inWater ? 7 : 1.1));
        direction += rotation;
        if (inWater) speed *= Math.exp(-Math.abs(rotation) * this.beach.turnLoss);
      }
      this.vx = Math.cos(direction) * speed;
      this.vz = Math.sin(direction) * speed / DEPTH_SPEED_SCALE;
      if (inWater) this.vx += flow.vx * dt * 0.32;
      if (!steer && !depthSteer && inWater) this.vz *= Math.exp(-dt * 4);
    } else {
      const control = this.recovery ? 0.35 : 1;
      const target = steer * 48 * control + flow.vx;
      const planing = !input.dive && !submerged && !this.recovery && steer * (this.vx - target) > 0;
      this.vx += (target - this.vx) * (1 - Math.exp(-dt * (steer ? planing ? 0.32 : 5 : 1.4)));
      if (depthSteer) this.vz += (depthSteer * 19 * control - this.vz) * (1 - Math.exp(-dt * 5));
      else this.vz *= Math.exp(-dt * 4);
    }
    if (touching && !input.dive && !submerged && !this.recovery) this.vx += GRAVITY * WAVE_PACE * slope / (1 + slope * slope) * dt;
    if (first && input.jump && standing && !input.dive && this.y >= water - 4 && this.y <= water + 8 && this.jumpCooldown === 0) {
      this.vy = clamp(waterVelocity + slope * this.vx + depthSlope * this.vz, -150, 65) - 62;
      this.jumping = true;
      this.jumpCooldown = 0.8;
    }

    this.vx = clamp(this.vx, -280, 280);
    this.vy = clamp(this.vy, -240, 200);
    this.vz = clamp(this.vz, -19, 19);
    this.x += this.vx * dt;
    this.z = clamp(this.z + this.vz * dt, 0, 100);
    if ((this.z === 0 && this.vz < 0) || (this.z === 100 && this.vz > 0)) this.vz = 0;
    this.y += this.vy * dt;
    if (supported && !this.jumping && !input.dive && !this.recovery) {
      const nextWater = this.surface(this.x, this.z);
      if (this.y > nextWater + 2) {
        this.y = nextWater + 2;
        this.vy = Math.min(this.vy, (nextWater - previousSurface) / dt);
      }
    }
    this.slowTime = standing && touching && this.boardSpeed < SETTLE_SPEED ? this.slowTime + dt : 0;
    if (this.slowTime > 0.7) this.posture = 'prone';
    this.standingBlend += ((this.posture === 'standing' ? 1 : 0) - this.standingBlend) * (1 - Math.exp(-dt * 7));
    const nextStance = standing ? clamp(this.stance + (Number(input.nose) - Number(input.tail)) * dt * 0.8, -0.8, 0.9) : 0;
    this.walking = nextStance !== this.stance;
    this.stance = nextStance;
    let hazard: Ocean['wipeoutCause'] = null;
    if (!input.dive && !submerged && !this.recovery) {
      for (const wave of this.waves) {
        const section = waveSection(wave, this.z);
        const bodyX = this.x + Math.cos(this.heading) * this.stance * 9;
        const roof = barrelRoof(wave, bodyX, this.z);
        const hitLip = this.y + 3 >= roof && this.y - (standing ? 22 : 6) <= roof + 3;
        const powerful = Math.max(wave.height, wave.amplitude) > this.beach.wipeoutHeight && section.height > 25 && wave.energy > 0.35;
        if (hitLip) {
          if (powerful && standing && wave.breaking > 0.05) { this.wipeOut('lip'); break; }
          this.vy += (24 - this.vy) * dt * 4;
        }
        const face = (section.center - this.x) / section.frontWidth;
        const onFace = face > 0.05 && face < 1.5 && Math.abs(this.y - this.surface(this.x, this.z)) < 10;
        if (!powerful || !standing || !onFace) continue;
        if (section.collapse > 0.18 && section.collapse < 0.95) hazard = 'closeout';
        else if (section.curl > 0.6 && wave.breaking > 0.05 && -this.vx < wave.speed * 0.3) hazard ??= 'stall';
      }
    }
    this.unstableTime = hazard ? this.unstableTime + dt : 0;
    if (hazard && this.unstableTime > 0.3) this.wipeOut(hazard);
    const floor = this.floor(this.x, this.z) - 9;
    if (this.y > floor) { this.y = floor; this.vy = Math.min(0, this.vy); }
    if (bottomDepth(this.beach, this.x, this.z) < 8 && !this.jumping) {
      this.onFoot = true; this.posture = 'prone'; this.recovery = 0;
    }
    if (Math.hypot(this.vx, this.vz) > 3) {
      const target = Math.atan2(-this.vz, this.vx);
      const turn = Math.atan2(Math.sin(target - this.heading), Math.cos(target - this.heading));
      this.heading += turn * (1 - Math.exp(-dt * 7));
    }
    const directionSlope = slope * Math.cos(this.heading) - depthSlope * Math.sin(this.heading);
    const targetAngle = input.dive || submerged ? clamp(-this.vy * 0.012, -0.35, 0.35)
      : touching ? -Math.atan(directionSlope) * 0.65 : clamp(-this.vy * 0.006, -0.65, 0.65);
    this.angle += (targetAngle - this.angle) * (1 - Math.exp(-dt * 7));
    this.speed += (clamp(Math.abs(this.vx) / 85 + lift * 0.25, 0, 1) - this.speed) * dt * 2;
    this.advanceParticles(dt, touching, submerged || input.dive);
    this.followCamera(dt);
  }

  private wipeOut(cause: NonNullable<Ocean['wipeoutCause']>) {
    if (this.recovery) return;
    this.recovery = 2;
    this.wipeoutCause = cause; this.unstableTime = 0;
    this.posture = 'prone'; this.stance = 0; this.jumping = false;
    this.vy = 20; this.vx *= 0.65;
  }

  private advanceOnFoot(dt: number, input: SurfInput) {
    const depth = bottomDepth(this.beach, this.x, this.z);
    const speed = depth > 2 ? 36 : 55;
    this.vx += ((Number(input.right) - Number(input.left)) * speed - this.vx) * (1 - Math.exp(-dt * 8));
    this.vz += ((Number(input.away) - Number(input.toward)) * 16 - this.vz) * (1 - Math.exp(-dt * 8));
    this.x += this.vx * dt;
    this.z = clamp(this.z + this.vz * dt, 0, 100);
    this.y = this.floor(this.x, this.z); this.vy = 0;
    this.walking = Math.hypot(this.vx, this.vz) > 3;
    this.paddling = false; this.stance = 0;
    if (this.walking) {
      const target = Math.atan2(-this.vz, this.vx);
      this.heading += Math.atan2(Math.sin(target - this.heading), Math.cos(target - this.heading)) * (1 - Math.exp(-dt * 9));
    }
    this.angle *= Math.exp(-dt * 8);
    this.standingBlend += (1 - this.standingBlend) * (1 - Math.exp(-dt * 7));
    if (depth > 18) {
      this.onFoot = false; this.walking = false; this.standingBlend = 0;
      this.y = this.surface(this.x, this.z); this.posture = 'prone';
    }
  }

  private followCamera(dt: number) {
    const targetX = this.x - OCEAN_WIDTH * 0.46 + clamp(this.vx * 0.8, -90, 90);
    this.camera.x += (targetX - this.camera.x) * (1 - Math.exp(-dt * 3));
    const targetY = Math.min(0, this.y - 170);
    this.camera.y += (targetY - this.camera.y) * (1 - Math.exp(-dt * 2));
  }

  private advanceWaves(dt: number) {
    if (this.time >= this.nextWave) {
      const id = this.waveNumber++;
      this.waves.push(createWave(id, this.beach, this.conditions, Math.max(this.beach.offshore, this.x + 1100)));
      this.wavesInSet++;
      if (this.wavesInSet >= this.beach.setSize + this.setNumber % 2) {
        this.nextWave = this.time + this.beach.period / WAVE_PACE + setLull(this.beach, this.conditions) * (0.9 + noise(id + 9) * 0.2);
        this.wavesInSet = 0;
        this.setNumber++;
      } else this.nextWave = this.time + this.beach.period / WAVE_PACE * (0.9 + noise(id + 3) * 0.2);
    }
    for (let i = this.waves.length - 1; i >= 0; i--) {
      const wave = this.waves[i];
      wave.age += dt;
      wave.x -= wave.speed * dt;
      evolveWave(wave, dt);
      const reach = wave.shape.backWidth * 3 + wave.shape.crestLength;
      if ((wave.breaking > 0.9 && wave.height < 0.8) || wave.x < -250 || wave.x + reach < this.camera.x - 1200
        || wave.x - reach > Math.max(this.beach.offshore + 100, this.x + 2400)) this.waves.splice(i, 1);
    }
  }

  private particle(kind: WaterParticle['kind'], x: number, y: number, z: number, vx: number, vy: number) {
    if (this.particles.length >= 320) return;
    const n = noise(this.particleNumber++);
    this.particles.push({ kind, x, y, z, vx, vy, life: kind === 'bubble' ? 4 : 1.3 + n * 1.4, size: n > 0.75 ? 2 : 1 });
  }

  private advanceParticles(dt: number, touching: boolean, submerged: boolean) {
    for (const wave of this.waves) {
      if (Math.abs(wave.x - this.x) > 850) continue;
      this.sprayDebt += dt * (wave.curl + wave.breaking) * 58;
      while (this.sprayDebt >= 1) {
        this.sprayDebt--;
        const n = noise(this.particleNumber + 28);
        const z = noise(this.particleNumber + 54) * 100;
        const lip = curlPoint(wave, 1, 0, z);
        const flow = this.flow(lip.x, z, lip.y);
        this.particle('spray', lip.x + n * 9, lip.y - 3, z, flow.vx - n * 28, flow.vy - 5 - noise(this.particleNumber) * 32);
      }
    }
    this.trailDebt += dt * (submerged ? 9 : touching ? Math.abs(this.vx) * 0.36 : 0);
    while (this.trailDebt >= 1) {
      this.trailDebt--;
      const n = noise(this.particleNumber + 81);
      const tail = -Math.cos(this.heading);
      this.particle(submerged ? 'bubble' : 'spray', this.x + tail * 9, this.y + (submerged ? -2 : 0), this.z,
        tail * (10 + n * 25), submerged ? -12 : -14 - n * 24);
    }
    for (let i = this.particles.length - 1; i >= 0; i--) {
      const p = this.particles[i];
      p.life -= dt;
      p.x += p.vx * dt;
      if (p.kind === 'spray') {
        p.vy += GRAVITY * dt;
        p.y += p.vy * dt;
        if (p.y >= this.surface(p.x, p.z) && p.vy > 0) {
          p.kind = 'foam'; p.life = 1.8; p.vx *= 0.3;
        }
      } else if (p.kind === 'foam') {
        p.y = this.surface(p.x, p.z) + 1;
        p.vx += (this.flow(p.x, p.z, p.y).vx - p.vx) * (1 - Math.exp(-dt * 3));
      } else {
        p.y += p.vy * dt;
        p.vx *= Math.exp(-dt * 1.5);
        if (p.y < this.surface(p.x, p.z)) p.life = 0;
      }
      if (p.life <= 0 || Math.abs(p.x - this.x) > 1000 || p.y > this.floor(p.x, p.z)) this.particles.splice(i, 1);
    }
  }

  draw(ctx: CanvasRenderingContext2D, reducedMotion: boolean) { drawOcean(ctx, this, reducedMotion); }
}
