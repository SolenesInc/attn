"use strict";
(() => {
  var __defProp = Object.defineProperty;
  var __defNormalProp = (obj, key, value) => key in obj ? __defProp(obj, key, { enumerable: true, configurable: true, writable: true, value }) : obj[key] = value;
  var __publicField = (obj, key, value) => __defNormalProp(obj, typeof key !== "symbol" ? key + "" : key, value);

  // app/src/components/SurfBreak/beaches.ts
  var defaultConditions = () => ({ size: "usual", rhythm: "steady" });
  var beaches = {
    cove: {
      id: "cove",
      name: "Sheltered cove",
      description: "Soft spilling waves. Plenty of time between sets.",
      slope: 0.085,
      offshore: 1500,
      amplitude: 16,
      period: 6,
      setSize: 2,
      lull: 30,
      peel: 0.2,
      hollow: 0.18,
      breakRun: 380,
      turnLoss: 0.02,
      wipeoutHeight: Infinity,
      sky: "#99c8f4",
      sand: "#d9bb85"
    },
    sandbar: {
      id: "sandbar",
      name: "Sandbar beach",
      description: "Changing peaks and a mix of soft and hollow waves.",
      slope: 0.09,
      offshore: 1900,
      amplitude: 40,
      period: 7,
      setSize: 3,
      lull: 18,
      peel: 0.45,
      hollow: 0.75,
      breakRun: 340,
      turnLoss: 0.06,
      wipeoutHeight: 90,
      sky: "#95c3f2",
      sand: "#d9bb85"
    },
    point: {
      id: "point",
      name: "Long point",
      description: "Long peeling faces. Room to carve and walk the board.",
      slope: 0.055,
      offshore: 2700,
      amplitude: 66,
      period: 11,
      setSize: 3,
      lull: 26,
      peel: 1.4,
      hollow: 0.9,
      breakRun: 1200,
      turnLoss: 0.04,
      wipeoutHeight: 100,
      sky: "#91bff0",
      sand: "#c4b083"
    },
    reef: {
      id: "reef",
      name: "Reef break",
      description: "Steep takeoffs and hollow tubes over a shallow shelf.",
      slope: 0.07,
      offshore: 2200,
      amplitude: 58,
      period: 8,
      setSize: 3,
      lull: 22,
      peel: 0.65,
      hollow: 1,
      breakRun: 260,
      turnLoss: 0.16,
      wipeoutHeight: 65,
      sky: "#8dbde5",
      sand: "#9e9980"
    },
    nazare: {
      id: "nazare",
      name: "Nazar\xE9",
      description: "Giant waves. Takeoff timing and your line matter.",
      slope: 0.115,
      offshore: 3400,
      amplitude: 115,
      period: 12,
      setSize: 2,
      lull: 35,
      peel: 0.85,
      hollow: 1,
      breakRun: 1250,
      turnLoss: 0.38,
      wipeoutHeight: 75,
      sky: "#91adbf",
      sand: "#bda783"
    }
  };
  var smooth = (v) => {
    const n = Math.max(0, Math.min(1, v));
    return n * n * (3 - 2 * n);
  };
  function bottomDepth(beach, x, z = 0) {
    const coast = x - Math.sin(z * 0.025) * 18;
    let depth = coast * beach.slope;
    if (coast <= 0) return Math.max(-45, depth);
    if (beach.id === "sandbar") depth -= 25 * Math.exp(-(((coast - 610 - z * 0.7) / 170) ** 2));
    if (beach.id === "point") depth += z * 0.22 * smooth(coast / 500);
    if (beach.id === "reef") depth += 85 * smooth((coast - 740 - z * 0.55) / 230);
    if (beach.id === "nazare") depth += 190 * smooth((coast - 1550) / 650) * (0.65 + z * 35e-4);
    return Math.max(0, depth + Math.sin(coast * 0.018) * Math.min(3, coast * 0.01));
  }
  function swellAmplitude(beach, conditions) {
    return beach.amplitude * { small: 0.7, usual: 1, large: 1.25 }[conditions.size];
  }
  function setLull(beach, conditions) {
    return beach.lull * { quiet: 1.8, steady: 1, frequent: 0.45 }[conditions.rhythm];
  }

  // app/src/components/SurfBreak/water.ts
  var OCEAN_WIDTH = 800;
  var OCEAN_HEIGHT = 450;
  var SEA_LEVEL = 310;
  var GRAVITY = 115;
  var WAVE_PACE = 0.5;
  var clamp = (n, min, max) => Math.max(min, Math.min(max, n));
  var noise = (n) => {
    const v = Math.sin(n * 127.1 + 311.7) * 43758.5453;
    return v - Math.floor(v);
  };
  var ease = (n) => {
    const v = clamp(n, 0, 1);
    return v * v * (3 - 2 * v);
  };
  function propagation(depth, period) {
    const d = Math.max(1, depth);
    const omega = 2 * Math.PI / period;
    let k = Math.max(omega * omega / GRAVITY, omega / Math.sqrt(GRAVITY * d));
    for (let i = 0; i < 5; i++) {
      const th = Math.tanh(k * d);
      k -= (GRAVITY * k * th - omega * omega) / (GRAVITY * (th + k * d * (1 - th * th)));
    }
    const speed = omega / k;
    const kd = Math.min(100, k * d);
    return { speed, groupSpeed: speed * 0.5 * (1 + 2 * kd / Math.sinh(2 * kd)), wavelength: 2 * Math.PI / k };
  }
  function createWave(id, beach = beaches.sandbar, conditions = defaultConditions(), x = beach.offshore) {
    const variation = noise(id + 17);
    const kind = beach.id === "cove" ? "roller" : beach.id === "point" ? "runner" : beach.id === "reef" || beach.id === "nazare" ? "hollow" : ["peeler", "roller", "hollow", "runner"][id % 4];
    const period = beach.period * (0.92 + variation * 0.16);
    const motion = propagation(bottomDepth(beach, x), period);
    const width = motion.wavelength;
    const long = kind === "roller" || kind === "runner";
    const shape = {
      kind,
      frontWidth: width * (long ? 0.15 : 0.1),
      backWidth: width * 0.25,
      crestLength: width * (long ? 0.15 : beach.id === "nazare" ? 0.12 : 0.035),
      barrelWidth: kind === "runner" ? 4.2 : beach.id === "nazare" ? 3.2 : 1.5,
      barrelHeight: long ? 0.85 : 1.08,
      maxCurl: kind === "roller" ? 0.18 : beach.hollow
    };
    const amplitude = swellAmplitude(beach, conditions) * (0.8 + variation * 0.35) * (kind === "roller" ? beach.id === "cove" ? 0.9 : 0.4 : 1);
    const wave = {
      id,
      x,
      age: 0,
      height: amplitude,
      amplitude,
      curl: 0,
      speed: motion.speed,
      shape,
      beach,
      period,
      energy: 1,
      breaking: 0,
      steepness: 0,
      initialGroupSpeed: motion.groupSpeed,
      depthGradient: 0
    };
    evolveWave(wave);
    return wave;
  }
  function evolveWave(wave, dt = 0) {
    const depth = Math.max(1, bottomDepth(wave.beach, wave.x, 40));
    wave.depthGradient = (bottomDepth(wave.beach, wave.x, 0) - bottomDepth(wave.beach, wave.x + 100 * wave.beach.peel, 100)) / depth * 25e-4;
    const motion = propagation(depth, wave.period);
    wave.speed = motion.speed * WAVE_PACE;
    const focusing = wave.beach.id === "nazare" ? 1 + 0.45 * Math.exp(-(((wave.x - 1450) / 480) ** 2)) : 1;
    const shoaled = wave.amplitude * Math.sqrt(clamp(wave.initialGroupSpeed / motion.groupSpeed, 0.6, 3.6)) * focusing;
    const ratio = shoaled / (depth * 0.78);
    wave.steepness = ease((ratio - 0.35) / 0.65);
    if (ratio > 0.95 || wave.breaking > 0) {
      wave.breaking = clamp(wave.breaking + dt * wave.speed / wave.beach.breakRun * clamp(ratio, 0.8, 2), 0, 1);
      wave.energy *= Math.exp(-dt * wave.speed / wave.beach.breakRun * (0.12 + collapseAt(wave.breaking) * 2.8));
    }
    const collapse = collapseAt(wave.breaking);
    const bore = Math.min(shoaled, depth * 0.75);
    wave.height = (shoaled * (1 - collapse) + bore * collapse) * Math.sqrt(wave.energy);
    wave.curl = wave.shape.maxCurl * ease((ratio - 0.5) / 0.55) * (1 - collapse);
  }
  var collapseAt = (breaking) => ease((breaking - 0.55) / 0.45);
  function waveSection(wave, z = 0) {
    const depthFactor = clamp(1 + wave.depthGradient * (z - 40), 0.7, 1.3);
    const height = wave.height * depthFactor;
    const curl = clamp(wave.curl * depthFactor ** 2, 0, wave.shape.maxCurl);
    const breaking = clamp(wave.breaking + (depthFactor - 1) * wave.breaking, 0, 1);
    const collapse = collapseAt(breaking);
    const steepness = clamp(wave.steepness * depthFactor ** 2, 0, 1) * (1 - collapse) * (0.4 + wave.shape.maxCurl * 0.6);
    const hollowing = curl * (1 - collapse);
    return {
      center: wave.x + z * wave.beach.peel - height * (steepness * 0.22 + hollowing * 0.12),
      frontWidth: wave.shape.frontWidth * (1.3 - steepness * 0.8 - hollowing * 0.1 + collapse * 0.7),
      backWidth: wave.shape.backWidth * (1 + steepness * 0.15 + collapse * 0.45),
      crestLength: wave.shape.crestLength * (1 - steepness * 0.45) + collapse * 65,
      steepness,
      breaking,
      collapse,
      hollowing,
      height,
      curl
    };
  }
  function waveLift(wave, x, z = 0) {
    const section = waveSection(wave, z);
    if (x >= section.center) {
      const d2 = Math.max(0, x - section.center - section.crestLength) / section.backWidth;
      return section.height * Math.exp(-(d2 ** 2));
    }
    const d = (section.center - x) / section.frontWidth;
    const rounded = Math.exp(-(d ** 2));
    const hollow = Math.exp(-d * 2.8) * (1 + d * 2.8);
    const trough = 0.16 * Math.exp(-(((d - 1.6) / 0.65) ** 2)) * (1 - Math.exp(-d * d * 5)) * (1 - section.breaking);
    return section.height * (rounded * (1 - section.steepness) + hollow * section.steepness - trough);
  }
  function waterSurface(waves, x, z, time) {
    let lift = 0;
    for (const wave of waves) lift += waveLift(wave, x, z);
    return SEA_LEVEL - lift + Math.sin(x * 0.035 + time * 1.15) * 0.8 + Math.sin(x * 0.017 - time * 0.6) * 0.7;
  }
  function sampleWater(waves, beach, x, z, time, y = SEA_LEVEL) {
    const surface = waterSurface(waves, x, z, time);
    const slopeX = (waterSurface(waves, x + 2, z, time) - waterSurface(waves, x - 2, z, time)) / 4;
    const slopeZ = (waterSurface(waves, x, z + 1, time) - waterSurface(waves, x, z - 1, time)) / 2;
    const depth = Math.max(12, bottomDepth(beach, x, z));
    let vx = 0;
    let surfaceVelocity = 0;
    for (const wave of waves) {
      const lift = waveLift(wave, x, z);
      const weight = clamp(lift / depth, -0.3, 0.9);
      vx -= wave.speed * weight * (0.75 + wave.breaking * 0.5);
      surfaceVelocity += wave.speed * (waveLift(wave, x - 2, z) - waveLift(wave, x + 2, z)) / 4;
    }
    const attenuation = Math.exp(-Math.max(0, y - surface) / Math.max(20, depth * 0.45));
    vx *= attenuation;
    const vz = -slopeZ * 4 * attenuation;
    return { surface, slopeX, slopeZ, vx, vz, vy: (surfaceVelocity + slopeX * vx + slopeZ * vz) * attenuation };
  }
  function curlPoint(wave, progress, thickness = 0, z = 0) {
    const { center, collapse, hollowing, height, curl } = waveSection(wave, z);
    const p = clamp(progress, 0, 1);
    const reach = wave.shape.barrelWidth * Math.max(32, height * 0.7) * curl * (1 + hollowing * 0.25 - collapse * 0.4);
    const crest = SEA_LEVEL - height;
    const relativeCurl = curl / Math.max(1e-3, wave.shape.maxCurl);
    const fall = clamp(ease((relativeCurl - 0.18) / 0.65) + collapse * 0.35, 0, 1);
    const rise = height * 0.08 * curl;
    const surfaceDrop = clamp(SEA_LEVEL - waveLift(wave, center - reach, z) - crest, height * 0.42, height * 1.08);
    const drop = surfaceDrop * (0.18 + fall * 0.82) * wave.shape.barrelHeight;
    const split = 0.72;
    const curve = p <= split ? cubicPoint(
      p / split,
      { x: center, y: crest },
      { x: center - reach * (0.2 + fall * 0.04), y: crest - rise * 0.75 },
      { x: center - reach * 0.7, y: crest - rise },
      { x: center - reach * 0.9, y: crest + drop * 0.035 }
    ) : cubicPoint(
      (p - split) / (1 - split),
      { x: center - reach * 0.9, y: crest + drop * 0.035 },
      { x: center - reach * 0.95, y: crest + drop * 0.08 },
      { x: center - reach * 0.985, y: crest + drop * 0.55 },
      { x: center - reach, y: crest + drop }
    );
    const { x, y, dx, dy } = curve;
    const length = Math.hypot(dx, dy) || 1;
    const rim = thickness * Math.sin(p * Math.PI / 2) * curl;
    return {
      x: x - dy / length * rim,
      y: y + dx / length * rim
    };
  }
  function cubicPoint(t, a, b, c, d) {
    const q = 1 - t;
    return {
      x: q ** 3 * a.x + 3 * q * q * t * b.x + 3 * q * t * t * c.x + t ** 3 * d.x,
      y: q ** 3 * a.y + 3 * q * q * t * b.y + 3 * q * t * t * c.y + t ** 3 * d.y,
      dx: 3 * q * q * (b.x - a.x) + 6 * q * t * (c.x - b.x) + 3 * t * t * (d.x - c.x),
      dy: 3 * q * q * (b.y - a.y) + 6 * q * t * (c.y - b.y) + 3 * t * t * (d.y - c.y)
    };
  }
  function barrelRoof(wave, x, z = 0) {
    const section = waveSection(wave, z);
    if (section.curl < 0.3 || x > section.center) return Infinity;
    const mouth = curlPoint(wave, 1, -5, z);
    if (x < section.center - (section.center - mouth.x) * 1.1 - 10) return Infinity;
    let previous = curlPoint(wave, 0, -5, z);
    let roof = Infinity;
    for (let i = 1; i <= 32; i++) {
      const point = curlPoint(wave, i / 32, -5, z);
      if (x >= Math.min(previous.x, point.x) && x <= Math.max(previous.x, point.x)) {
        const fraction = (x - previous.x) / (point.x - previous.x || 1);
        roof = Math.min(roof, previous.y + (point.y - previous.y) * fraction);
      }
      previous = point;
    }
    return roof;
  }
  function project(x, y, z, camera = { x: 0, y: 0 }) {
    const scale = 1 - z * 22e-4;
    return {
      x: OCEAN_WIDTH / 2 + (x - camera.x - OCEAN_WIDTH / 2) * scale + z * 0.32,
      y: SEA_LEVEL + (y - camera.y - SEA_LEVEL) * scale - z * 0.42,
      scale
    };
  }
  function seaFloor(x, z = 0, beach = beaches.sandbar) {
    return SEA_LEVEL + bottomDepth(beach, x, z);
  }

  // app/src/components/SurfBreak/paintSurfer.ts
  function drawSurfer(ctx, ocean, t) {
    const { heading, angle, stance } = ocean;
    const forward = Math.cos(heading);
    const across = Math.sin(heading);
    const swimming = ocean.state === "submerged" || ocean.state === "recovering";
    const standing = ocean.onFoot ? 1 : swimming ? 0 : ocean.standingBlend;
    const sizeScale = ocean.project(ocean.x, ocean.y, ocean.z).scale;
    const point = (u, v, height) => ocean.project(
      ocean.x + u * Math.cos(angle) * forward - v * across,
      ocean.y - height - u * Math.sin(angle),
      ocean.z - u * Math.cos(angle) * across - v * forward
    );
    const pixel2 = (p, size, color) => {
      ctx.fillStyle = color;
      const width = size * sizeScale;
      ctx.fillRect(Math.round(p.x - width / 2), Math.round(p.y - width / 2), Math.ceil(width), Math.ceil(width));
    };
    const line = (a, b, width, color) => {
      const steps = Math.ceil(Math.max(Math.abs(b.x - a.x), Math.abs(b.y - a.y)));
      for (let i = 0; i <= steps; i++) {
        const f = steps ? i / steps : 0;
        pixel2({ x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f }, width, color);
      }
    };
    const polygon = (points, color) => {
      ctx.fillStyle = color;
      ctx.beginPath();
      points.forEach((p, i) => {
        if (!i) ctx.moveTo(Math.round(p.x), Math.round(p.y));
        else ctx.lineTo(Math.round(p.x), Math.round(p.y));
      });
      ctx.closePath();
      ctx.fill();
    };
    const board = [[-14, 0], [-10, -3], [7, -3], [15, -1], [17, 0], [13, 2], [5, 3], [-10, 3]];
    const boardPoint = (u, v, height) => ocean.onFoot ? point(u, v - 4, height + 11 + u * 0.15) : point(u, v, height);
    polygon(board.map(([u, v]) => boardPoint(u, v, -1.5)), "#bc9875");
    polygon(board.map(([u, v]) => boardPoint(u, v, 0)), swimming ? "#9cceaa" : "#f3e2b7");
    line(boardPoint(9, -2, 0.5), boardPoint(9, 2, 0.5), 1.5, "#dd9276");
    line(boardPoint(-9, 0, -1), boardPoint(-10, 0, -4), 2, "#284d63");
    const foot = stance * 9;
    const stride = ocean.walking ? Math.sin(t * 9) * 2 : 0;
    const pose = (prone, upright) => point(
      prone[0] + (upright[0] - prone[0]) * standing,
      prone[1] + (upright[1] - prone[1]) * standing,
      prone[2] + (upright[2] - prone[2]) * standing
    );
    const hip = pose([-4, 0, 3], [foot, 0, 8.5]);
    const shoulder = pose([3, 0, 3.5], [foot + 0.5, 0, 16]);
    const head = pose([7, 0, 5], [foot + 1, 0, 20.5]);
    const backKnee = pose([-8, -1.2, 2], [foot - 2, -1.2, 5]);
    const frontKnee = pose([-7, 1.2, 2], [foot + 1, 1.2, 5]);
    line(pose([-12, -1.2, 1.5], [foot - 4 - stride, -1.2, 1]), backKnee, 2.5, "#c99879");
    line(backKnee, hip, 3, "#25475d");
    line(pose([-11, 1.2, 1.5], [foot + 3 + stride, 1.2, 1]), frontKnee, 2.5, "#eeb58c");
    line(frontKnee, hip, 3, "#25475d");
    polygon([
      pose([-4, -2.3, 3], [foot - 1, -2.3, 9]),
      pose([-4, 2.3, 3], [foot + 1, 2.3, 9]),
      pose([3, 2.8, 3.5], [foot + 1, 2.8, 16]),
      pose([3, -2.8, 3.5], [foot, -2.8, 16])
    ], "#e97767");
    line(hip, shoulder, 4, "#e97767");
    pixel2(head, 4.5, "#eeb58c");
    line({ x: head.x - 2, y: head.y - 2 }, { x: head.x + 1, y: head.y - 2 }, 2, "#263b50");
    if (across < -0.3) pixel2({ x: head.x, y: head.y - 0.5 }, 4, "#263b50");
    else pixel2({ x: head.x + forward * 2, y: head.y }, 1, "#745447");
    if (ocean.onFoot) {
      line(point(0, -2.8, 15), point(3, -5, 10), 2, "#c99879");
      line(point(1, 2.8, 15), point(-stride, 4, 9), 2, "#eeb58c");
    } else if (ocean.armsCrossed && standing > 0.85) {
      line(point(foot + 0.5, -2.8, 15), point(foot + 2.3, -2, 12), 2, "#c99879");
      line(point(foot + 2.3, -2, 12), point(foot + 2.5, 2.4, 13.5), 2, "#eeb58c");
      line(point(foot + 0.5, 2.8, 15), point(foot + 2.8, 2.2, 12), 2, "#eeb58c");
      line(point(foot + 2.8, 2.2, 12), point(foot + 2.5, -2.4, 14), 2, "#d7a17d");
    } else {
      const stroke = ocean.paddling || swimming ? Math.sin(t * 5) : 0;
      const backElbow = pose([1 + stroke * 2, -5, 2], [foot - 4, -5, 13]);
      const frontElbow = pose([1 - stroke * 2, 5, 2], [foot + 5, 4, 13]);
      line(pose([3, -2.8, 3.5], [foot, -2.8, 15]), backElbow, 2, "#c99879");
      line(backElbow, pose([-1 + stroke * 5, -6, stroke], [foot - 7, -5, 15]), 2, "#c99879");
      line(pose([3, 2.8, 3.5], [foot + 1, 2.8, 15]), frontElbow, 2, "#eeb58c");
      line(frontElbow, pose([-1 - stroke * 5, 6, -stroke], [foot + 8, 4, 15]), 2, "#eeb58c");
    }
  }

  // app/src/components/SurfBreak/boardTrail.ts
  function boardTrailPoint(travel, spread = 0, reach = 1) {
    const width = 1 + Math.sin(Math.PI * travel) * 8;
    return [
      -17 - travel * 108 * reach,
      travel * travel * 4 + spread * width,
      travel * travel * 62 * reach - Math.abs(spread) * width * 0.4
    ];
  }
  function drawBoardTrail(ctx, project2, standing) {
    const reach = 0.25 + standing * 0.75;
    ctx.save();
    const alpha = ctx.globalAlpha;
    for (let step = 159; step >= 0; step--) {
      const travel = step / 160;
      const fade = (1 - travel) ** 0.65;
      for (let ribbon = -3; ribbon <= 3; ribbon++) {
        const n = noise(Math.floor(step / 2) * 37 + ribbon * 113 + 67);
        if (Math.abs(ribbon) > 1 && n > fade * 0.75 || travel > 0.65 && n > fade) continue;
        const point = project2(boardTrailPoint(travel, ribbon / 3, reach));
        ctx.globalAlpha = alpha * fade * (Math.abs(ribbon) > 1 ? 0.65 : 0.95);
        ctx.fillStyle = ribbon === 0 ? "#e9ffe7" : Math.abs(ribbon) === 1 ? "#d7f7dd" : "#a2e8cf";
        ctx.fillRect(Math.floor(point.x / 2) * 2, Math.floor(point.y / 2) * 2, n > 0.65 ? 4 : 2, 2);
      }
    }
    ctx.restore();
  }

  // app/src/components/SurfBreak/paintSurferModel.ts
  function drawSurferModel(ctx, ocean, t, scale = 1) {
    const anchor = ocean.project(ocean.x, ocean.y, ocean.z);
    const forward = Math.cos(ocean.heading);
    const across = Math.sin(ocean.heading);
    const submerged = ocean.state === "submerged" || ocean.state === "recovering";
    const standing = ocean.onFoot ? 1 : submerged ? 0 : ocean.standingBlend;
    const foot = ocean.stance * 7;
    const stroke = ocean.paddling || submerged ? Math.sin(t * 5) : 0;
    const pixel2 = 1;
    const project2 = ([u, v, h]) => {
      const p = ocean.project(
        ocean.x + u * Math.cos(ocean.angle) * forward - v * across,
        ocean.y - h - u * Math.sin(ocean.angle),
        ocean.z - u * Math.cos(ocean.angle) * across - v * forward
      );
      return { x: anchor.x + (p.x - anchor.x) * scale, y: anchor.y + (p.y - anchor.y) * scale };
    };
    const posed = (upright, prone) => project2(upright.map((value, i) => prone[i] + (value + (i === 0 ? foot : 0) - prone[i]) * standing));
    const polygon = (points, color) => {
      ctx.fillStyle = color;
      const top = Math.floor(Math.min(...points.map((p) => p.y)) / pixel2) * pixel2;
      const bottom = Math.ceil(Math.max(...points.map((p) => p.y)) / pixel2) * pixel2;
      for (let y = top; y < bottom; y += pixel2) {
        const crossings = [];
        for (let i = 0; i < points.length; i++) {
          const a = points[i];
          const b = points[(i + 1) % points.length];
          if (a.y <= y + pixel2 / 2 !== b.y <= y + pixel2 / 2) {
            crossings.push(a.x + (y + pixel2 / 2 - a.y) * (b.x - a.x) / (b.y - a.y));
          }
        }
        crossings.sort((a, b) => a - b);
        for (let i = 0; i + 1 < crossings.length; i += 2) {
          const x = Math.round(crossings[i] / pixel2) * pixel2;
          const width = Math.round(crossings[i + 1] / pixel2) * pixel2 - x;
          if (width > 0) ctx.fillRect(x, y, width, pixel2);
        }
      }
    };
    const limb = (a, b, start, end, color) => {
      const length = Math.hypot(b.x - a.x, b.y - a.y) || 1;
      const nx = (b.y - a.y) / length * scale * anchor.scale / 2;
      const ny = (a.x - b.x) / length * scale * anchor.scale / 2;
      polygon([
        { x: a.x + nx * start, y: a.y + ny * start },
        { x: b.x + nx * end, y: b.y + ny * end },
        { x: b.x - nx * end, y: b.y - ny * end },
        { x: a.x - nx * start, y: a.y - ny * start }
      ], color);
    };
    const mesh = (upright, prone, color) => polygon(upright.map((p, i) => posed(p, prone[i])), color);
    if (!ocean.onFoot && !submerged) {
      const shadow = project2([0, 0, -3]);
      ctx.fillStyle = "#176a7c";
      ctx.fillRect(Math.round(shadow.x - 20 * scale), Math.round(shadow.y), Math.round(39 * scale), 2);
      drawBoardTrail(ctx, project2, standing);
    }
    const deck = [[-17, 0, 0], [-13, -3, 0], [10, -3, 0], [17, -1, 0.6], [19, 0, 1], [15, 2, 0], [8, 3, 0], [-13, 3, 0]];
    const boardPoint = (p) => project2(ocean.onFoot ? [p[0], p[1] - 5, p[2] + 12 + p[0] * 0.12] : p);
    polygon([[-12, 0, -1], [-9, 0, -1], [-13, 0, -5]].map((p) => boardPoint(p)), "#23485c");
    polygon(deck.map(([u, v, h]) => boardPoint([u, v, h - 1.6])), "#bc9875");
    polygon(deck.map(boardPoint), "#f3e2b7");
    limb(boardPoint([-14, 0, 0.5]), boardPoint([16, 0, 1]), 1.1, 0.7, "#fff5d2");
    limb(boardPoint([10, -2, 0.8]), boardPoint([10, 2, 0.8]), 1.5, 1.5, "#d17c69");
    const hip = posed([-1, 0, 10], [-4, 0, 3.5]);
    const rearKnee = posed([-6, -1.6, 5.8], [-8, -1.6, 2.4]);
    const leadKnee = posed([9, 1.6, 6.5], [-7, 1.6, 2.4]);
    const rearAnkle = posed([-11, -1.6, 1], [-13, -1.6, 1.5]);
    const leadAnkle = posed([8, 1.6, 1], [-12, 1.6, 1.5]);
    limb(hip, rearKnee, 5, 3.5, "#1f344d");
    limb(rearKnee, rearAnkle, 3.1, 2, "#b47759");
    limb(rearAnkle, posed([-8, -1.6, 0.8], [-15, -1.6, 1.5]), 2.2, 1.7, "#d9976d");
    limb(hip, leadKnee, 5.6, 4.1, "#284965");
    limb(leadKnee, leadAnkle, 3.3, 2.1, "#edb184");
    limb(leadAnkle, posed([12, 1.6, 0.8], [-14, 1.6, 1.5]), 2.2, 1.7, "#f4c699");
    const rearShoulder = posed([4, -2.6, 18.5], [3, -2.6, 3.8]);
    const leadShoulder = posed([5, 2.6, 18.8], [3, 2.6, 3.8]);
    const rearElbow = posed([-2, -4, 17], [stroke * 2, -5, 2]);
    const rearHand = posed([-9, -4, 13.5], [-2 + stroke * 5, -6, stroke]);
    if (!ocean.armsCrossed || standing < 0.85) {
      limb(rearShoulder, rearElbow, 3.6, 2.7, "#b96453");
      limb(rearElbow, rearHand, 2.5, 1.6, "#cf8d66");
    }
    mesh(
      [[-4, 0, 10], [2, 0, 9], [8, 0, 18], [5, 0, 21], [1, 0, 20]],
      [[-5, -2.7, 3], [-5, 2.7, 3], [4, 2.8, 4], [5, 0, 4.2], [3, -2.8, 4]],
      "#b85c53"
    );
    mesh(
      [[-2, 1, 11], [2, 1, 10], [8, 1, 18], [5, 1, 20], [2, 1, 19]],
      [[-4, -1.6, 3.5], [-4, 2.8, 3.5], [4, 2.8, 4.3], [5, 0, 4.5], [3, -1.6, 4.3]],
      "#e97767"
    );
    mesh(
      [[2, -2, 10], [2, 2, 10], [7, 2.8, 19], [7, -2.8, 19]],
      [[-4, -2.5, 3.2], [-4, 2.5, 3.2], [4, 2.8, 4], [4, -2.8, 4]],
      across > 0 ? "#ef866d" : "#c56355"
    );
    limb(posed([3, 1, 19.6], [2, 1.5, 4.4]), posed([6.5, 1, 18.7], [4, 1.5, 4.5]), 1.4, 1.3, "#f99478");
    limb(posed([-3, 0, 10], [-5, 0, 3]), posed([2, 0, 9.5], [-3, 0, 3]), 2.2, 2.2, "#213d56");
    limb(posed([5, 0, 20], [4.5, 0, 4]), posed([6, 0, 22], [6, 0, 5]), 3, 2.8, "#cf8d66");
    mesh(
      [[4, 0, 22], [4, 0, 26.3], [8, 0, 26.5], [9, 0, 24], [10, 0, 23.7], [9, 0, 22.6], [8, 0, 21.7]],
      [[5, 0, 3.7], [5, 0, 7.5], [9, 0, 7.5], [10, 0, 5.8], [11, 0, 5.3], [10, 0, 4.4], [8, 0, 3.7]],
      "#edb184"
    );
    mesh(
      [[3.5, 0.2, 23.5], [3, 0.2, 26.5], [5, 0.2, 27.5], [8.5, 0.2, 27], [9, 0.2, 25.4], [5.4, 0.2, 25.4], [5.5, 0.2, 23]],
      [[5, 0.2, 5], [4, 0.2, 7], [6, 0.2, 8.4], [9, 0.2, 8], [10, 0.2, 6.7], [6.6, 0.2, 6.7], [6.5, 0.2, 4.8]],
      "#293345"
    );
    mesh(
      [[7, -2, 22], [7, 2, 22], [7, 2, 25.5], [7, -2, 25.5]],
      [[8, -2, 4], [8, 2, 4], [8, 2, 7], [8, -2, 7]],
      across > 0 ? "#edb184" : "#293345"
    );
    limb(posed([7, -2.2, 26], [8, -2.2, 7.6]), posed([7, 2.2, 26], [8, 2.2, 7.6]), 1.5, 1.5, "#293345");
    if (across < -0.3) {
      limb(posed([5, 0.3, 22.5], [6, 0.3, 4.7]), posed([5.5, 0.3, 25.8], [6, 0.3, 7]), 4, 4, "#293345");
    } else {
      limb(posed([8, 0.4, 24.2], [9, 0.4, 6]), posed([8.6, 0.4, 24.2], [9.6, 0.4, 6]), 0.7, 0.7, "#594636");
    }
    if (ocean.armsCrossed && standing > 0.85) {
      const rear = posed([4, -1, 15], [0, 0, 0]);
      const lead = posed([7, 2, 15], [0, 0, 0]);
      limb(rearShoulder, rear, 3, 2.3, "#d9976d");
      limb(rear, posed([7, 2.6, 17], [0, 0, 0]), 2.5, 1.6, "#edb184");
      limb(leadShoulder, lead, 3.4, 2.6, "#e97767");
      limb(lead, posed([3, -1.6, 17], [0, 0, 0]), 2.6, 1.8, "#f4c699");
    } else {
      const elbow = posed([11, 3.5, 15.6], [1 - stroke * 2, 5, 2]);
      limb(leadShoulder, posed([8, 3, 17], [2.5, 3.5, 3]), 4, 3.4, "#f0886d");
      limb(posed([8, 3, 17], [2.5, 3.5, 3]), elbow, 2.8, 2.2, "#edb184");
      limb(elbow, posed([17, 3.5, 13.8], [-2 - stroke * 5, 6, -stroke]), 2.3, 1.5, "#f4c699");
    }
  }

  // app/src/components/SurfBreak/paintWave.ts
  var snap = (n) => Math.round(n / 2) * 2;
  function tubeOpening(ctx, wave, z, camera) {
    const bounds = { left: Infinity, right: -Infinity, top: Infinity, bottom: -Infinity };
    let first = true;
    const vertex = (x, y) => {
      const p = project(x, y, z, camera);
      bounds.left = Math.min(bounds.left, p.x);
      bounds.right = Math.max(bounds.right, p.x);
      bounds.top = Math.min(bounds.top, p.y);
      bounds.bottom = Math.max(bounds.bottom, p.y);
      if (first) ctx.moveTo(snap(p.x), snap(p.y));
      else ctx.lineTo(snap(p.x), snap(p.y));
      first = false;
    };
    ctx.beginPath();
    for (let i = 0; i <= 40; i++) {
      const arc = curlPoint(wave, i / 40, -5, z);
      vertex(arc.x, arc.y);
    }
    const lip = curlPoint(wave, 1, -5, z);
    const { center, height } = waveSection(wave, z);
    for (let x = lip.x; x < center; x += 2) {
      vertex(x, SEA_LEVEL - waveLift(wave, x, z));
    }
    vertex(center, SEA_LEVEL - height);
    ctx.closePath();
    return bounds;
  }
  function drawWaveFace(ctx, screenRect, wave, t, camera = { x: 0, y: 0 }) {
    const rect = (x, y, w, h, color) => screenRect(snap(x - camera.x), snap(y - camera.y), w, h, color);
    if (wave.height < 3) return;
    const { center, frontWidth, backWidth, crestLength, curl } = waveSection(wave);
    const start = Math.max(center - frontWidth * 2.5, camera.x - 20);
    const end = Math.min(center + crestLength + backWidth * 1.65, camera.x + 820);
    for (let column = Math.floor((start - center) / 4); column * 4 + center <= end; column++) {
      const x = center + column * 4;
      const lift = waveLift(wave, x);
      if (lift < 6) continue;
      const top = SEA_LEVEL - lift;
      const seed = column + wave.id * 97;
      const cap = lift * (0.2 + Math.sin(column * 0.22) * 0.045 + noise(Math.floor(seed / 3)) * 0.07);
      const shade = lift * (0.52 + Math.sin(column * 0.17 + 4) * 0.08 + noise(Math.floor(seed / 4)) * 0.08);
      rect(x, top, 4, lift + 1, "#59c8c5");
      rect(x, top + cap, 4, lift - cap + 1, "#3198ac");
      rect(x, top + shade, 4, lift - shade + 1, "#286d9c");
      for (let patch = 0; patch < 3; patch++) {
        const depth = noise(seed * 11 + patch * 31);
        rect(
          x,
          top + depth * lift,
          4 + noise(seed + patch) * 5,
          3 + noise(seed * 7 + patch) * 9,
          depth < 0.25 ? "#68cecb" : depth < 0.6 ? "#349dae" : "#2c79a0"
        );
      }
    }
    const lip = curl > 0.25 && wave.shape.maxCurl >= 0.45 ? curlPoint(wave, 1) : null;
    for (let column = Math.floor((start - center) / 15); column * 15 + center < end; column++) {
      const origin = center + column * 15 + noise(column) * 5;
      const height = waveLift(wave, origin);
      if (height < 8) continue;
      if (lip && origin > lip.x - frontWidth * 0.12 && origin < center + crestLength + backWidth * 0.2) continue;
      const steps = Math.min(60, Math.ceil(height / 3));
      for (let j = 0; j < steps; j++) {
        if (noise(column * 23 + Math.floor(j / 3)) < 0.22) continue;
        const flow = (j / steps + t * 0.075 + noise(column + wave.id) * 0.4) % 1;
        const x = origin - (flow * 0.15 + flow * flow * 0.65) * Math.min(height, frontWidth);
        const y = SEA_LEVEL - height + flow * (height + 3);
        if (y < SEA_LEVEL - waveLift(wave, x) || y > SEA_LEVEL + 5) continue;
        const color = flow < 0.18 || flow > 0.86 || column % 3 === 0 ? "#d3f9e9" : "#88ded4";
        rect(x, y, 2 + noise(column + j * 7) * 3, 2 + noise(j + 31) * 2, color);
      }
    }
    ctx.save();
    ctx.globalAlpha = clamp(wave.height / 25, 0, 1);
    for (let i = 0; i < 32; i++) {
      const x = start + noise(i + wave.id * 29) * (end - start);
      if (waveLift(wave, x) < 10) continue;
      rect(x, SEA_LEVEL + 1 + noise(i + 18) * 4, 3 + noise(i) * 8, 1, "#8be2db");
    }
    ctx.restore();
  }
  function drawTube(ctx, rect, wave, t, camera) {
    if (wave.shape.maxCurl < 0.45 || wave.curl <= 0.2 || wave.height < 3) return;
    ctx.save();
    ctx.globalAlpha *= clamp((wave.curl - 0.2) / 0.35, 0, 1) * (1 - waveSection(wave).collapse);
    const bounds = tubeOpening(ctx, wave, 0, camera);
    ctx.clip();
    rect(bounds.left - 2, bounds.top - 2, bounds.right - bounds.left + 4, bounds.bottom - bounds.top + 4, "#255c89");
    for (let i = 0; i < 180; i++) {
      const depth = noise(i + 211);
      const x = bounds.left + noise(i + wave.id * 19) * (bounds.right - bounds.left);
      const y = bounds.top + depth * (bounds.bottom - bounds.top);
      rect(snap(x), snap(y), 4 + noise(i + 75) * 8, 4 + noise(i + 17) * 12, depth < 0.55 ? "#214f7c" : "#2d6d95");
    }
    for (let i = 0; i <= 72; i++) {
      const progress = i / 72;
      const arc = curlPoint(wave, progress, -7 - noise(i + wave.id) * wave.height * 0.08, 0);
      const p = project(arc.x, arc.y, 0, camera);
      rect(snap(p.x), snap(p.y), 4 + noise(i + 12) * 7, 3 + noise(i + 43) * 6, "#183e64");
    }
    const { center, frontWidth } = waveSection(wave);
    for (let i = 0; i < 60; i++) {
      const x = center - frontWidth * (0.3 + noise(i + wave.id) * 2);
      const p = project(x, SEA_LEVEL - waveLift(wave, x) - 2 + Math.sin(t * 0.7 + i) * 2, 0, camera);
      rect(snap(p.x), snap(p.y), 5 + noise(i) * 12, 2 + noise(i + 14) * 3, "#60c9c6");
    }
    ctx.restore();
  }
  function drawCurlBody(ctx, rect, wave, t, z, camera) {
    const section = waveSection(wave, z);
    const thickness = Math.max(10, section.height * 0.24) * section.curl;
    const point = (arc, rim) => {
      const p = curlPoint(wave, arc, rim, z);
      return project(p.x, p.y, z, camera);
    };
    ctx.save();
    ctx.beginPath();
    for (let i = 0; i <= 64; i++) {
      const p = point(i / 64, thickness);
      if (!i) ctx.moveTo(snap(p.x), snap(p.y));
      else ctx.lineTo(snap(p.x), snap(p.y));
    }
    for (let i = 64; i >= 0; i--) {
      const p = point(i / 64, -5);
      ctx.lineTo(snap(p.x), snap(p.y));
    }
    ctx.closePath();
    ctx.fillStyle = "#3198ac";
    ctx.fill();
    ctx.clip();
    for (let stripe = 0; stripe < 12; stripe++) {
      const across = stripe / 11;
      for (let i = 0; i < 65; i++) {
        const flow = (i / 65 + t * 0.065) % 1;
        const p = point(flow, -5 + (thickness + 5) * across);
        const n = noise(Math.floor(i / 3) + stripe * 19 + wave.id);
        const lit = across > 0.72;
        const color = lit ? n > 0.45 ? "#b4eddb" : "#62cfcc" : n > 0.7 ? "#79d6cf" : "#3aadb5";
        rect(snap(p.x), snap(p.y), 3 + n * 5, 3 + noise(i + stripe) * 6, color);
      }
    }
    ctx.restore();
    for (let i = 0; i < 75; i++) {
      if (noise(Math.floor(i / 3) + wave.id) < 0.18) continue;
      const p = point((i / 75 + t * 0.065) % 1, thickness);
      rect(snap(p.x), snap(p.y), 3 + noise(i) * 5, 2 + noise(i + 20) * 3, "#e3fff0");
    }
  }
  function drawLip(ctx, rect, wave, t, z, camera = { x: 0, y: 0 }) {
    if (wave.height < 2) return;
    const { center, frontWidth, backWidth, crestLength, breaking } = waveSection(wave, z);
    const start = Math.max(center - frontWidth * 2.5, camera.x - 220);
    const end = Math.min(center + crestLength + backWidth * 1.65, camera.x + 1020);
    for (let x = start; x < end; x += 3) {
      if (waveLift(wave, x, z) < 4) continue;
      const p = project(x, SEA_LEVEL - waveLift(wave, x, z), z, camera);
      const n = noise(Math.floor((x - center) / 3) + wave.id);
      if (n > 0.16) rect(snap(p.x), snap(p.y) - n * 2, 4 + n * 4, 2 + n * 2 + breaking * 4, "#dbffef");
    }
    if (breaking > 0) {
      for (let i = 0; i < 90; i++) {
        const x = center - frontWidth * 1.4 + noise(i + wave.id) * (frontWidth * 1.4 + crestLength);
        const p = project(x, SEA_LEVEL - waveLift(wave, x, z), z, camera);
        rect(p.x, p.y + noise(i + 92) * 10 * breaking, 3 + breaking * noise(i + 22) * 12, 1 + breaking * 2, "#b4f3e4");
      }
    }
    if (wave.curl < 0.25 || wave.shape.maxCurl < 0.45) return;
    drawCurlBody(ctx, rect, wave, t, z, camera);
    const lip = curlPoint(wave, 1, 0, z);
    for (let i = 0; i < 45; i++) {
      const spread = noise(i + wave.id * 7);
      const x = lip.x - spread * 45 + noise(i + 22) * 20;
      const y = SEA_LEVEL - waveLift(wave, x, z);
      const p = project(x, y, z, camera);
      rect(snap(p.x), snap(p.y) - noise(i + 31) * 5, 4 + spread * 12, 2 + noise(i + 56) * 5, "#d3f9e9");
    }
  }

  // app/src/components/SurfBreak/barrelGeometry.ts
  var barrelDomain = 1.15;
  function barrelDisplayU(frame, localU) {
    return frame.curlSide === "right" ? barrelDomain - localU : localU;
  }
  function barrelLocalU(frame, displayU) {
    return frame.curlSide === "right" ? barrelDomain - displayU : displayU;
  }
  function barrelSectionAt(frame, u) {
    if (frame.treatment === "curtain") return lipDrivenSectionAt(frame, u);
    const roof = 0.32 * Math.exp(-Math.max(0, u) / 0.043) - frame.roundness * 0.43 * Math.exp(-(((u - 0.34) / 0.34) ** 2)) + Math.sin(u * 8) * 8e-3;
    const ceiling = roof + 0.19 + Math.sin(u * 5) * 0.015;
    const bottom = 1.02 - 0.93 * ease((u - 0.34) / 0.8) ** 1.65;
    return { roof, ceiling, bottom };
  }
  function lipDrivenSectionAt(frame, u) {
    const root = barrelLipAt(frame, 0);
    const tip = barrelLipAt(frame, 1);
    const span = Math.max(1e-3, root.u - tip.u);
    if (u <= tip.u) {
      return { roof: tip.v, ceiling: tip.v + 0.08, bottom: 1.04 };
    }
    if (u >= root.u) {
      const shoulder = ease((u - root.u) / Math.max(1e-3, barrelDomain - root.u));
      const roof = root.v + shoulder * 0.045;
      return { roof, ceiling: roof + 0.09, bottom: roof + 0.13 + shoulder * 0.08 };
    }
    const lip = barrelLipAtU(frame, u);
    const across = clamp((u - tip.u) / span, 0, 1);
    const closingFace = ease(across) ** 1.45;
    const bottom = 1.04 + (root.v + 0.105 - 1.04) * closingFace;
    const thickness = 0.055 + 0.045 * Math.sin(Math.PI * across);
    return { roof: lip.v, ceiling: lip.v + thickness, bottom: Math.max(lip.v + thickness + 0.02, bottom) };
  }
  function barrelSection(frame, u) {
    return barrelSectionAt(frame, barrelLocalU(frame, u));
  }
  function barrelSample(frame, u, v) {
    const localU = barrelLocalU(frame, u);
    const { roof, ceiling, bottom } = barrelSectionAt(frame, localU);
    const mouth = frame.treatment === "curtain" ? 0 : v > 0.31 && v < 1.05 ? 0.125 * Math.sin(Math.PI * (v - 0.31) / 0.74) ** 0.8 : 0;
    const inside = localU >= Math.max(mouth, barrelFront(frame, v)) && localU <= barrelDomain && v >= roof && v <= 1.14;
    const depth = (v - ceiling) / Math.max(0.04, bottom - ceiling);
    const shelter = frame.treatment === "curtain" ? lipDrivenShelter(frame, localU, v, roof, bottom) : (1 - ease((localU - 0.5) / 0.36)) * ease((v - roof - 0.09) / 0.1);
    return {
      inside,
      localU,
      roof,
      ceiling,
      bottom,
      depth,
      shelter,
      hollow: inside && depth >= 0 && depth <= 1 && shelter > 0.18
    };
  }
  function lipDrivenShelter(frame, u, v, roof, bottom) {
    const root = barrelLipAt(frame, 0);
    const tip = barrelLipAt(frame, 1);
    const across = clamp((u - tip.u) / Math.max(1e-3, root.u - tip.u), 0, 1);
    const underLip = ease((v - roof - 0.035) / Math.max(0.04, (bottom - roof) * 0.2));
    return Math.sin(Math.PI * across) ** 0.35 * underLip;
  }
  function barrelFront(frame, v) {
    if (frame.treatment === "curtain") {
      const root = barrelLipAt(frame, 0.72);
      const tip = barrelLipAt(frame, 1);
      if (v < root.v) return root.u;
      if (v > tip.v) return tip.u;
      let low2 = 0.72;
      let high2 = 1;
      for (let i = 0; i < 14; i++) {
        const mid = (low2 + high2) / 2;
        if (barrelLipAt(frame, mid).v < v) low2 = mid;
        else high2 = mid;
      }
      return barrelLipAt(frame, (low2 + high2) / 2).u;
    }
    if (v < barrelLipAt(frame, 0).v || v > barrelLipAt(frame, 1).v) return 0;
    let low = 0;
    let high = 1;
    for (let i = 0; i < 10; i++) {
      const mid = (low + high) / 2;
      if (barrelLipAt(frame, mid).v < v) low = mid;
      else high = mid;
    }
    return barrelLipAt(frame, (low + high) / 2).u;
  }
  function barrelLipAt(frame, t) {
    if (frame.treatment === "curtain") return fallingLipAt(frame, t);
    const root = 0.15 + frame.roundness * 0.18;
    const roof = barrelSectionAt(frame, root).roof;
    const drop = 0.46 + frame.roundness * 0.39;
    const a = { u: root, v: roof };
    const b = { u: 0.075, v: roof + 0.035 };
    const c = { u: -0.055 * frame.roundness, v: drop - 0.28 };
    const d = { u: 0.022 + frame.roundness * 0.012, v: drop };
    const s = 1 - t;
    return {
      u: s ** 3 * a.u + 3 * s * s * t * b.u + 3 * s * t * t * c.u + t ** 3 * d.u,
      v: s ** 3 * a.v + 3 * s * s * t * b.v + 3 * s * t * t * c.v + t ** 3 * d.v,
      du: 3 * s * s * (b.u - a.u) + 6 * s * t * (c.u - b.u) + 3 * t * t * (d.u - c.u),
      dv: 3 * s * s * (b.v - a.v) + 6 * s * t * (c.v - b.v) + 3 * t * t * (d.v - c.v)
    };
  }
  function fallingLipAt(frame, t) {
    const fall = clamp(frame.lipFall, 0, 1);
    const roofEnd = { u: 0.9 - fall * 0.75, v: 0.05 };
    const split = 0.72;
    if (t <= split) {
      const p2 = cubic(
        t / split,
        { u: 1.02, v: 0.02 },
        { u: 0.94 - fall * 0.22, v: 0.02 - fall * 0.05 },
        { u: 0.92 - fall * 0.6, v: 0.03 - fall * 0.07 },
        roofEnd
      );
      return { ...p2, du: p2.du / split, dv: p2.dv / split };
    }
    const p = cubic(
      (t - split) / (1 - split),
      roofEnd,
      { u: 0.9 - fall * 0.83, v: 0.06 + fall * 0.03 },
      { u: 0.9 - fall * 0.855, v: 0.11 + fall * 0.44 },
      { u: 0.9 - fall * 0.865, v: 0.15 + fall * 0.7 }
    );
    return { ...p, du: p.du / (1 - split), dv: p.dv / (1 - split) };
  }
  function cubic(t, a, b, c, d) {
    const s = 1 - t;
    return {
      u: s ** 3 * a.u + 3 * s * s * t * b.u + 3 * s * t * t * c.u + t ** 3 * d.u,
      v: s ** 3 * a.v + 3 * s * s * t * b.v + 3 * s * t * t * c.v + t ** 3 * d.v,
      du: 3 * s * s * (b.u - a.u) + 6 * s * t * (c.u - b.u) + 3 * t * t * (d.u - c.u),
      dv: 3 * s * s * (b.v - a.v) + 6 * s * t * (c.v - b.v) + 3 * t * t * (d.v - c.v)
    };
  }
  function barrelLipAtU(frame, u) {
    const root = barrelLipAt(frame, 0);
    const tip = barrelLipAt(frame, 1);
    if (u >= root.u) return root;
    if (u <= tip.u) return tip;
    let low = 0;
    let high = 1;
    for (let i = 0; i < 16; i++) {
      const mid = (low + high) / 2;
      if (barrelLipAt(frame, mid).u > u) low = mid;
      else high = mid;
    }
    return barrelLipAt(frame, (low + high) / 2);
  }
  function barrelLip(frame, t) {
    const lip = barrelLipAt(frame, t);
    const orientation = frame.curlSide === "right" ? -1 : 1;
    return { ...lip, u: barrelDisplayU(frame, lip.u), du: lip.du * orientation };
  }
  function flowBend(frame, travel) {
    if (frame.treatment === "curtain") return 0;
    const bend = (0.17 + frame.roundness * 0.04) * Math.sin(Math.PI * travel) - 0.24 * travel * travel;
    const peel = frame.peelDirection === "right" ? -1 : 1;
    const curl = frame.curlSide === "right" ? -1 : 1;
    return bend * peel * curl;
  }
  function barrelLipContour(frame, t, inset) {
    const lip = barrelLip(frame, t);
    const section = barrelSection(frame, lip.u);
    const taper = Math.sin(Math.PI * clamp(t, 0, 1)) ** 0.6;
    const depth = Math.min(0.18, Math.max(0, section.bottom - section.roof) * 0.34) * clamp(inset, 0, 1) * taper;
    return { u: lip.u, v: lip.v + depth };
  }
  function barrelLipImpact(frame) {
    const tip = barrelLip(frame, 1);
    return { u: tip.u, v: barrelSection(frame, tip.u).bottom };
  }
  function barrelFlowPoint(frame, lane, travel) {
    const point = barrelFlowPointAt(frame, lane, travel);
    return { u: barrelDisplayU(frame, point.u), v: point.v };
  }
  function barrelFlowCoordinates(frame, u, v) {
    const localU = barrelLocalU(frame, u);
    const { roof } = barrelSectionAt(frame, localU);
    let travel = (v - roof) / (1.04 - roof);
    if (travel > 0 && travel < 1) {
      let low = 0;
      let high = 1;
      for (let i = 0; i < 10; i++) {
        travel = (low + high) / 2;
        const point = barrelFlowPointAt(frame, localU - flowBend(frame, travel), travel);
        if (point.v < v) low = travel;
        else high = travel;
      }
      travel = (low + high) / 2;
    }
    return { lane: localU - flowBend(frame, travel), travel };
  }
  function barrelFlowPointAt(frame, lane, travel) {
    const u = lane + flowBend(frame, travel);
    const roof = barrelSectionAt(frame, lane).roof;
    return { u, v: roof + travel * (1.04 - roof) };
  }
  function curtainLanes(frame) {
    if (frame.treatment === "curtain") return { left: 0, width: 1 };
    const left = barrelLipAt(frame, 0).u + 0.12;
    return { left, width: 0.34 };
  }
  function curtainFlowPoint(frame, across, travel) {
    if (frame.treatment === "curtain") {
      const t = 0.72 + clamp(travel, 0, 1) * 0.28;
      const lip = barrelLip(frame, t);
      const length = Math.hypot(lip.du * frame.width, lip.dv * frame.height) || 1;
      const orientation = frame.curlSide === "right" ? -1 : 1;
      const offset = (clamp(across, 0, 1) - 0.5) * 2 * frame.height * (0.04 + frame.lipFall * 0.055);
      const nx = orientation * lip.dv * frame.height / length;
      const ny = -orientation * lip.du * frame.width / length;
      return {
        u: lip.u + nx * offset / frame.width,
        v: lip.v + ny * offset / frame.height
      };
    }
    const { left, width } = curtainLanes(frame);
    return barrelFlowPoint(frame, left + across * width, travel);
  }
  function curtainSample(frame, u, v) {
    if (frame.treatment === "curtain") {
      let closest = { distance: Infinity, travel: 0, across: 0 };
      for (let i = 0; i <= 80; i++) {
        const travel2 = i / 80;
        const center = curtainFlowPoint(frame, 0.5, travel2);
        const edge = curtainFlowPoint(frame, 1, travel2);
        const radius = Math.hypot((edge.u - center.u) * frame.width, (edge.v - center.v) * frame.height) || 1;
        const distance = Math.hypot((u - center.u) * frame.width, (v - center.v) * frame.height);
        if (distance >= closest.distance) continue;
        const lip = barrelLip(frame, 0.72 + travel2 * 0.28);
        const length = Math.hypot(lip.du * frame.width, lip.dv * frame.height) || 1;
        const nx = (frame.curlSide === "right" ? -1 : 1) * lip.dv * frame.height / length;
        const ny = -(frame.curlSide === "right" ? -1 : 1) * lip.du * frame.width / length;
        const projection = ((u - center.u) * frame.width * nx + (v - center.v) * frame.height * ny) / radius;
        closest = { distance, travel: travel2, across: 0.5 + projection * 0.5 };
      }
      return {
        lane: closest.travel,
        travel: closest.travel,
        across: closest.across,
        inside: frame.curtain > 0 && closest.distance <= frame.height * (0.04 + frame.lipFall * 0.055)
      };
    }
    const { lane, travel } = barrelFlowCoordinates(frame, u, v);
    const { left, width } = curtainLanes(frame);
    const across = (lane - left) / width;
    return {
      lane,
      travel,
      across,
      inside: frame.curtain > 0 && across >= 0 && across <= 1 && travel >= 0 && travel <= 1 && barrelSample(frame, u, v).inside
    };
  }

  // app/src/components/SurfBreak/paintBarrel.ts
  var ink = {
    deep: "#0d3959",
    shadow: "#104564",
    blue: "#145774",
    inner: "#196c84",
    face: "#167f92",
    water: "#168f9e",
    light: "#30aaa9",
    glass: "#65cbbb",
    mint: "#a2e8cf",
    foam: "#d7f7dd",
    white: "#e9ffe7"
  };
  var pixel = 2;
  var cavity = [
    ink.deep,
    "#0e3d5d",
    "#0f4161",
    ink.shadow,
    "#124c6c",
    ink.blue,
    "#175e7b",
    "#186580",
    ink.inner,
    "#18758b",
    ink.face,
    "#168799",
    ink.water
  ];
  var snap2 = (n) => Math.floor(n / pixel) * pixel;
  function raster(ctx, frame, camera, colorAt) {
    const left = Math.max(0, snap2(frame.left - camera.x));
    const right = Math.min(OCEAN_WIDTH, frame.left + frame.width * 1.15 - camera.x);
    const top = Math.max(0, snap2(frame.floor - frame.height * 1.5 - camera.y));
    const bottom = Math.min(OCEAN_HEIGHT, frame.floor + frame.height * 0.14 - camera.y);
    for (let y = top; y < bottom; y += pixel) {
      let previous = null;
      let start = left;
      const flush = (x) => {
        if (previous) {
          ctx.fillStyle = previous;
          ctx.fillRect(start, y, x - start, pixel);
        }
      };
      for (let x = left; x < right; x += pixel) {
        const u = (x + camera.x - frame.left) / frame.width;
        const v = (y + camera.y - frame.floor) / frame.height + 1;
        const color = colorAt(u, v);
        if (color !== previous) {
          flush(x);
          previous = color;
          start = x;
        }
      }
      flush(Math.ceil(right / pixel) * pixel);
    }
  }
  function flowingTexture(seed, lane, travel) {
    const column = Math.floor(lane * 118);
    const row = Math.floor(travel * 36 + noise(column + seed) * 1.5);
    const cell = noise(column * 619 + row * 37 + seed * 71);
    const fold = Math.sin(lane * 53 + Math.sin(lane * 19) * 0.6);
    return { cell, fold, patch: cell > 0.62 ? 1 : cell < 0.18 ? -0.65 : 0 };
  }
  function barrelColor(frame, u, v) {
    const shape = barrelSample(frame, u, v);
    if (!shape.inside) return null;
    const { lane, travel } = barrelFlowCoordinates(frame, u, v);
    const texture = flowingTexture(frame.seed, lane, travel);
    const n = texture.cell;
    const flow = texture.fold * 0.023;
    const grain = flow + texture.patch * 0.015;
    const fromRoof = (v - shape.roof) / (1.4 - frame.roundness * 0.3);
    if (fromRoof < 0.025 + grain * 0.3) return n > 0.2 ? ink.foam : ink.mint;
    if (fromRoof < 0.052 + grain) return ink.glass;
    if (fromRoof < 0.105 + grain) return ink.light;
    if (fromRoof < 0.155 + grain) return ink.water;
    if (shape.hollow) {
      const rounded = Math.hypot((shape.localU - 0.32) / 0.72, (shape.depth - 0.31) / 1.04);
      const long = Math.max(0, shape.depth - 0.18) * 0.65 + Math.max(0, shape.localU - 0.4) * 0.5;
      const shade = long + (rounded - long) * frame.roundness + flow + (n - 0.5) * 0.055 + (1 - shape.shelter) * 0.48 + Math.max(0, shape.localU - 0.46) * 0.52 + ease((shape.depth - 0.78) / 0.22) * 0.24;
      const band = Math.floor(clamp((shade - 0.15) * 15 + texture.patch * 1.15, 0, cavity.length - 1));
      if (band === 0 && texture.patch !== 0) return texture.patch > 0 ? "#0e3d5d" : "#0c3656";
      return cavity[band];
    }
    if (v > 1.02) return v < 1.065 + flow ? ink.light : ink.water;
    if (v > shape.bottom && v < shape.bottom + 0.075 + grain) return ink.glass;
    const fold = texture.fold * 0.05 + texture.patch * 0.035;
    if (v < shape.bottom + 0.18 + fold) return texture.patch > 0.5 ? "#46b8b0" : ink.light;
    if (v < shape.bottom + 0.4 + fold) return texture.patch > 0.5 ? "#209da4" : ink.water;
    return texture.patch > 0.5 ? ink.water : ink.face;
  }
  function dab(ctx, frame, camera, u, v, w, h, color) {
    const x = snap2(frame.left + u * frame.width - camera.x);
    const y = snap2(frame.floor + (v - 1) * frame.height - camera.y);
    if (x + w < 0 || x > OCEAN_WIDTH || y + h < 0 || y > OCEAN_HEIGHT) return;
    ctx.fillStyle = color;
    ctx.fillRect(x, y, w, h);
  }
  function drawFoam(ctx, frame, camera) {
    if (frame.treatment === "curtain") {
      drawLipDrivenFoam(ctx, frame, camera);
      return;
    }
    for (let strand = 0; strand < 67; strand++) {
      const lane = 0.055 + strand * 0.022 + noise(strand + frame.seed) * 0.01;
      const major = strand % 4 === 0;
      for (let step = 0; step < 128; step++) {
        const t = step / 127;
        const { u, v } = barrelFlowPoint(frame, lane, t);
        const sample = barrelSample(frame, u, v);
        if (!sample.inside) continue;
        const shadowed = sample.hollow && sample.depth > 0.08 && sample.depth < 0.9 && sample.shelter > 0.85;
        const n = noise(strand * 149 + Math.floor(t * 54) + frame.seed);
        if (shadowed && lane < 0.62 || n < (major ? 0.1 : 0.56)) continue;
        const bright = t < 0.07 || t > 0.93;
        const color = bright ? ink.foam : major ? n > 0.38 || t > 0.62 ? ink.mint : ink.glass : n > 0.9 ? ink.glass : ink.light;
        dab(ctx, frame, camera, u, v, major && n > 0.73 ? 4 : 2, 2, color);
      }
    }
    for (let row = 0; row < 5; row++) {
      for (let cell = 0; cell < 225; cell++) {
        const lane = 4e-3 + cell / 195;
        const t = row * 0.018 + noise(cell + frame.seed) * 0.012;
        const { u, v } = barrelFlowPoint(frame, lane, t);
        const n = noise(cell * 31 + row * 113 + frame.seed);
        if (n < 0.22 || !barrelSample(frame, u, v).inside) continue;
        dab(ctx, frame, camera, u, v, n > 0.55 ? 4 : 2, 2, row < 2 ? ink.foam : n > 0.55 ? ink.mint : ink.glass);
      }
    }
    for (let row = 0; row < 5; row++) {
      for (let cell = 0; cell < 200; cell++) {
        const lane = cell / 150;
        const n = noise(cell * 29 + row * 71 + frame.seed);
        const { u, v } = barrelFlowPoint(frame, lane, 0.92 + row * 0.031 + n * 0.012);
        if (n < 0.38 || !barrelSample(frame, u, v).inside) continue;
        dab(ctx, frame, camera, u, v, n > 0.8 ? 4 : 2, 2, row === 2 || n > 0.86 ? ink.foam : ink.mint);
      }
    }
  }
  function drawLipDrivenFoam(ctx, frame, camera) {
    for (let band = 0; band < 9; band++) {
      const inset = 0.08 + band * 0.082;
      const major = band % 3 === 0;
      for (let step = 0; step <= 180; step++) {
        const t = step / 180;
        const { u, v } = barrelLipContour(frame, t, inset);
        const n = noise(band * 193 + Math.floor(t * 73) + frame.seed);
        if (n < (major ? 0.26 : 0.69) || !barrelSample(frame, u, v).inside) continue;
        const color = band < 2 ? ink.mint : major ? ink.glass : n > 0.91 ? ink.light : "#299fa3";
        dab(ctx, frame, camera, u, v, major && n > 0.82 ? 4 : 2, 2, color);
      }
    }
    const impact = barrelLipImpact(frame);
    for (let i = 0; i < 115; i++) {
      const n = noise(i * 37 + frame.seed);
      const spread = noise(i * 71 + frame.seed) ** 1.8;
      const u = impact.u + spread * 0.31;
      const v = impact.v - noise(i * 29 + frame.seed) * (0.025 + (1 - spread) * 0.08);
      if (n < 0.31 || !barrelSample(frame, u, v).inside) continue;
      dab(
        ctx,
        frame,
        camera,
        u,
        v,
        n > 0.78 ? 6 : 2,
        n > 0.88 ? 4 : 2,
        n > 0.84 ? ink.foam : n > 0.54 ? ink.mint : ink.glass
      );
    }
  }
  function drawBreakingLip(ctx, frame, camera) {
    const orientation = frame.curlSide === "right" ? -1 : 1;
    for (let i = 0; i <= 160; i++) {
      const t = i / 160;
      const { u, v, du, dv } = barrelLip(frame, t);
      const dx = du * frame.width;
      const dy = dv * frame.height;
      const length = Math.hypot(dx, dy);
      const thickness = frame.height * (frame.treatment === "curtain" ? (0.07 + frame.lipFall * 0.045) * (0.45 + Math.sin(t * Math.PI) * 0.75) : (0.08 + frame.roundness * 0.085) * (1 + Math.sin(t * Math.PI) * 0.35));
      for (let d = thickness; d >= 0; d -= pixel) {
        const n = noise(Math.floor(i / 4) + Math.floor(d / 4) * 67 + frame.seed);
        const color = d < 2 ? ink.foam : d < thickness * 0.35 ? n > 0.38 ? ink.foam : ink.mint : d < thickness * 0.6 ? n > 0.62 ? ink.mint : ink.glass : d < thickness * 0.82 ? n > 0.72 ? ink.glass : ink.light : ink.water;
        dab(
          ctx,
          frame,
          camera,
          u + orientation * dy / length * d / frame.width,
          v - orientation * dx / length * d / frame.height,
          4,
          2,
          color
        );
      }
    }
  }
  function drawBarrelBack(ctx, frame, camera) {
    if (frame.width <= 0 || frame.height <= 0) return;
    ctx.save();
    for (let row = 0; row < 12; row++) {
      const width = 0.78 + Math.sin(row / 12 * Math.PI) * 0.4;
      const edgeA = barrelDisplayU(frame, 0.4 - width / 2);
      const edgeB = barrelDisplayU(frame, 0.4 + width / 2);
      dab(
        ctx,
        frame,
        camera,
        Math.min(edgeA, edgeB),
        1.04 + row * 0.024,
        Math.ceil(frame.width * width / 2) * 2,
        2,
        row % 3 ? "#158a9b" : "#1a92a1"
      );
    }
    raster(ctx, frame, camera, (u, v) => barrelColor(frame, u, v));
    drawFoam(ctx, frame, camera);
    drawBreakingLip(ctx, frame, camera);
    for (let i = 0; i < 120; i++) {
      const lane = noise(i + 311) * 1.3;
      const { u, v } = barrelFlowPoint(frame, lane, 1.02 + noise(i + 83) * 0.08);
      dab(
        ctx,
        frame,
        camera,
        u,
        v,
        4 + Math.floor(noise(i + 9) * 6) * 2,
        2,
        i % 4 === 0 ? ink.glass : ink.light
      );
    }
    ctx.restore();
  }
  function drawBarrelFront(ctx, frame, camera) {
    if (frame.width <= 0 || frame.height <= 0 || frame.curtain <= 0) return;
    if (frame.treatment === "curtain") {
      drawFallingLipFront(ctx, frame, camera);
      return;
    }
    ctx.save();
    ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.38;
    raster(ctx, frame, camera, (u, v) => {
      const sample = curtainSample(frame, u, v);
      if (!sample.inside) return null;
      const texture = flowingTexture(frame.seed, sample.lane, sample.travel);
      const edge = ease(Math.min(sample.across, 1 - sample.across) / 0.13);
      if (texture.cell > edge) return null;
      return texture.patch > 0.5 ? ink.light : texture.fold > 0.2 ? "#259da3" : ink.water;
    });
    ctx.restore();
    ctx.save();
    ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.8;
    for (let strand = 0; strand < 13; strand++) {
      const across = 0.04 + strand / 12 * 0.92;
      const major = strand % 3 === 0;
      for (let step = 0; step < 128; step++) {
        const t = step / 127;
        const { u, v } = curtainFlowPoint(frame, across, t);
        if (!barrelSample(frame, u, v).inside) continue;
        const n = noise(strand * 57 + Math.floor(t * 47) + frame.seed);
        if (n < (major ? 0.12 : 0.57)) continue;
        dab(
          ctx,
          frame,
          camera,
          u,
          v,
          major && n > 0.8 ? 4 : 2,
          2,
          t < 0.05 || t > 0.9 ? ink.foam : major ? ink.mint : ink.glass
        );
      }
    }
    for (let i = 0; i < 70; i++) {
      const { u, v } = curtainFlowPoint(frame, noise(i + frame.seed * 3), 0.98 + noise(i + 78) * 0.06);
      if (!barrelSample(frame, u, v).inside) continue;
      dab(
        ctx,
        frame,
        camera,
        u,
        v,
        2 + Math.floor(noise(i + 111) * 3) * 2,
        2,
        ease(noise(i + 29)) > 0.5 ? ink.foam : ink.mint
      );
    }
    ctx.restore();
  }
  function drawFallingLipFront(ctx, frame, camera) {
    ctx.save();
    ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.34;
    for (let stripe = 0; stripe < 9; stripe++) {
      const across = stripe / 8;
      const major = stripe === 0 || stripe === 4 || stripe === 8;
      for (let step = 0; step <= 110; step++) {
        const travel = step / 110;
        const { u, v } = curtainFlowPoint(frame, across, travel);
        const n = noise(stripe * 131 + Math.floor(travel * 67) + frame.seed);
        if (n < (major ? 0.23 : 0.62)) continue;
        dab(
          ctx,
          frame,
          camera,
          u,
          v,
          major && n > 0.82 ? 4 : 2,
          2,
          travel > 0.9 || n > 0.9 ? ink.foam : major ? ink.mint : ink.glass
        );
      }
    }
    ctx.restore();
    ctx.save();
    ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.72;
    const impact = barrelLipImpact(frame);
    for (let i = 0; i < 48; i++) {
      const n = noise(i * 41 + frame.seed);
      if (n < 0.28) continue;
      dab(
        ctx,
        frame,
        camera,
        impact.u + noise(i + 21) * 0.2,
        impact.v - noise(i + 43) * 0.055,
        n > 0.8 ? 6 : 2,
        2,
        n > 0.68 ? ink.foam : ink.mint
      );
    }
    ctx.restore();
  }

  // app/src/components/SurfBreak/paintBarrelSetting.ts
  function drawBarrelSetting(rect, ocean) {
    const horizon = 246;
    const surface = ocean.project(ocean.camera.x, SEA_LEVEL + 24).y;
    for (let band = 0; band < 24; band++) {
      const f = band / 23;
      rect(
        0,
        band * horizon / 24,
        OCEAN_WIDTH,
        horizon / 24 + 1,
        `rgb(${Math.round(107 + f * 14)}, ${Math.round(173 + f * 14)}, ${Math.round(233 + f * 9)})`
      );
    }
    const islandX = 55 - ocean.camera.x * 0.06;
    for (let x = 0; x < 110; x += 3) {
      const peak = Math.max(0, (1 - x / 110) * 24 + Math.sin(x * 0.12) * 4);
      const ridge = Math.round((horizon - peak) / 3) * 3;
      rect(islandX + x, ridge, 3, horizon - ridge, "#294d70");
      if (noise(x + 81) > 0.65) rect(islandX + x, ridge + 6, 3, 3, "#375b7b");
    }
    for (let y = horizon; y < surface; y += 3) {
      const depth = (y - horizon) / (surface - horizon);
      rect(
        0,
        y,
        OCEAN_WIDTH,
        3,
        `rgb(${Math.round(38 - depth * 15)}, ${Math.round(171 - depth * 27)}, ${Math.round(185 - depth * 22)})`
      );
    }
    for (let i = 0; i < 460; i++) {
      const y = horizon + noise(i + 30) * (surface - horizon);
      const x = noise(i + 17) * OCEAN_WIDTH;
      rect(x, y, 3 + noise(i + 89) * 17, 1, i % 4 ? "#249eaf" : "#32aebe");
    }
    rect(0, surface, OCEAN_WIDTH, OCEAN_HEIGHT - surface, "#167f91");
    for (let y = surface; y < OCEAN_HEIGHT; y += 3) {
      const depth = (y - surface) / (OCEAN_HEIGHT - surface);
      rect(
        0,
        y,
        OCEAN_WIDTH,
        3,
        `rgb(${Math.round(21 - depth * 1)}, ${Math.round(128 - depth * 17)}, ${Math.round(146 - depth * 12)})`
      );
    }
    rect(0, surface, OCEAN_WIDTH, 2, "#4bbaaf");
    for (let i = 0; i < 64; i++) {
      rect(
        noise(i + 97) * OCEAN_WIDTH,
        surface + noise(i + 35) * 61,
        2,
        1 + Number(i % 3 === 0),
        i % 4 ? "#198b99" : "#28a4ad"
      );
    }
  }

  // app/src/components/SurfBreak/paintOcean.ts
  var waterColors = ["#83e9e4", "#4dd3db", "#34bace", "#28a5c1", "#258fb5", "#247da9", "#246da0"];
  function drawOcean(ctx, ocean, reducedMotion, artwork) {
    ctx.imageSmoothingEnabled = false;
    const rect = (x, y, w, h, color) => {
      if (w <= 0 || h <= 0) return;
      ctx.fillStyle = color;
      ctx.fillRect(Math.round(x), Math.round(y), Math.ceil(w), Math.ceil(h));
    };
    const t = reducedMotion ? 0 : ocean.time;
    const { camera } = ocean;
    if (artwork) drawBarrelSetting(rect, ocean);
    else drawOceanSetting(ctx, rect, ocean, t);
    const visibleWaves = artwork ? [] : ocean.waves.filter((wave) => wave.x + wave.shape.crestLength + wave.shape.backWidth * 2 > camera.x - 250 && wave.x - wave.shape.frontWidth * 3 < camera.x + OCEAN_WIDTH + 250);
    for (const wave of visibleWaves) {
      drawWaveFace(ctx, rect, wave, t, camera);
      drawTube(ctx, rect, wave, t, camera);
    }
    for (const barrel2 of artwork?.barrels ?? []) drawBarrelBack(ctx, barrel2, camera);
    drawFish(rect, t, ocean);
    drawGround(ctx, rect, ocean);
    const barrel = ocean.barrel;
    const cover = ocean.cover;
    let paintedSurfer = false;
    for (let z = 100; z >= 0; z -= 5) {
      if (!paintedSurfer && ocean.z >= z) {
        if (artwork?.detailedSurfer) drawSurferModel(ctx, ocean, t, artwork.surferScale);
        else {
          if (artwork?.surferScale) {
            const feet = ocean.project(ocean.x, ocean.y, ocean.z);
            ctx.save();
            ctx.translate(feet.x, feet.y);
            ctx.scale(artwork.surferScale, artwork.surferScale);
            ctx.translate(-feet.x, -feet.y);
          }
          drawSurfer(ctx, ocean, t);
          if (artwork?.surferScale) ctx.restore();
        }
        if (ocean.state === "wading") {
          const feet = ocean.project(ocean.x, ocean.y, ocean.z);
          const water = ocean.project(ocean.x, ocean.surface(ocean.x, ocean.z), ocean.z);
          ctx.save();
          ctx.globalAlpha = 0.45;
          rect(feet.x - 7, water.y, 14, feet.y - water.y + 1, "#55d3d9");
          ctx.restore();
        }
        paintedSurfer = true;
      }
      for (const wave of visibleWaves) {
        if (z === 0 || z === 100) {
          ctx.save();
          ctx.globalAlpha = (z === 0 ? 1 : 0.45) * (z < ocean.z && barrel === wave ? 1 - cover * 0.72 : 1);
          drawLip(ctx, rect, wave, t, z, camera);
          ctx.restore();
        }
      }
      for (const p of ocean.particles) {
        if (p.z < z || p.z >= z + 5) continue;
        const position = ocean.project(p.x, p.y, p.z);
        if (p.kind === "bubble") rect(position.x, position.y, p.size + 1, 1, "#8ce2dc");
        else rect(position.x, position.y, p.kind === "foam" ? p.size + 2 : p.size, p.size, p.life > 0.5 ? "#e3fff0" : "#8ce3df");
      }
    }
    for (const barrel2 of artwork?.barrels ?? []) drawBarrelFront(ctx, barrel2, camera);
    if (cover > 0 && barrel) {
      ctx.save();
      ctx.globalAlpha = cover * 0.07;
      tubeOpening(ctx, barrel, 0, camera);
      ctx.fillStyle = "#6ed9df";
      ctx.fill();
      ctx.restore();
    }
  }
  function drawOceanSetting(ctx, rect, ocean, t) {
    drawSky(ctx, rect, t, ocean);
    const { camera } = ocean;
    const horizon = ocean.project(0, SEA_LEVEL, 100).y;
    rect(0, horizon, OCEAN_WIDTH, OCEAN_HEIGHT - horizon, "#38b9cb");
    for (let screenX = 0; screenX < OCEAN_WIDTH; screenX += 2) {
      const x = screenX + camera.x;
      const top = ocean.surface(x) - camera.y;
      const floor = ocean.floor(x) - camera.y;
      for (let band = 0; band < waterColors.length; band++) {
        const y = top + band * (7 + band * 0.6);
        if (y < floor) rect(screenX, y, 2, floor - y + 1, waterColors[band]);
      }
      if (floor > top) rect(screenX, top, 2, 1, "#b4f7e8");
    }
    const firstTile = Math.floor(camera.x / OCEAN_WIDTH);
    for (let tile = firstTile; tile <= firstTile + 1; tile++) {
      for (let i = 0; i < 280; i++) {
        const seed = tile * 281 + i;
        const x = tile * OCEAN_WIDTH + noise(seed + 30) * OCEAN_WIDTH;
        const top = ocean.surface(x);
        const floor = ocean.floor(x);
        if (floor <= top + 10) continue;
        const depth = noise(seed + 219);
        const y = top + 5 + depth * Math.min(180, floor - top - 9);
        rect(
          x - camera.x,
          y - camera.y,
          2 + noise(seed + 17) * 5,
          1 + noise(seed + 13) * 3,
          depth < 0.2 ? "#69d9dc" : depth < 0.5 ? "#35aec4" : "#2985b0"
        );
      }
    }
    drawWaterSurface(ctx, rect, ocean, t);
  }
  function drawSky(ctx, rect, t, ocean) {
    const sky = ocean.beach.id === "nazare" ? ["#91adbf", "#9bb5c4", "#a5beca", "#b4c9d0", "#c0d1d5", "#ccdad9"] : ["#91bff0", "#95c3f2", "#99c8f4", "#9dccf4", "#a3d1f4", "#b0daf3"];
    const skyHeight = Math.max(SEA_LEVEL, SEA_LEVEL - ocean.camera.y);
    for (let i = 0; i < sky.length; i++) rect(0, i * skyHeight / sky.length, OCEAN_WIDTH, skyHeight / sky.length + 1, sky[i]);
    for (let y = -13; y <= 13; y++) {
      const width = Math.sqrt(169 - y * y);
      rect(112 - width, 75 + y, width * 2, 1, "#f2f3cf");
    }
    for (let i = 0; i < 4; i++) {
      const span = OCEAN_WIDTH + 180;
      const x = ((i * 241 + 260 - t * 0.5) % span + span) % span - 90;
      const y = SEA_LEVEL - 101 + noise(i + 49) * 41;
      const width = 25 + noise(i + 11) * 25;
      rect(x, y, width + 15, 3, "#cbe8f5");
      rect(x + 4, y - 3, width + 7, 4, "#edf9f5");
      rect(x + 12, y - 6, width - 7, 4, "#edf9f5");
      rect(x + 19, y - 8, width - 23, 3, "#edf9f5");
    }
    ctx.save();
    ctx.translate(-ocean.camera.x * 0.06, -ocean.camera.y * 0.78);
    ctx.fillStyle = "#789bb5";
    ctx.beginPath();
    ctx.moveTo(0, SEA_LEVEL - 12);
    for (let x = 0; x <= 290; x += 5) ctx.lineTo(x, Math.round(SEA_LEVEL - 18 - Math.sin(x * 0.022) ** 2 * 11));
    ctx.lineTo(290, SEA_LEVEL - 11);
    ctx.fill();
    ctx.fillStyle = "#3c657c";
    ctx.beginPath();
    ctx.moveTo(0, SEA_LEVEL - 11);
    for (let x = 0; x <= 212; x += 4) {
      const height = Math.max(0, 33 * Math.sin((x + 30) * 0.013) + Math.sin(x * 0.053) * 6);
      ctx.lineTo(x, Math.round(SEA_LEVEL - 13 - height));
    }
    ctx.lineTo(212, SEA_LEVEL - 11);
    ctx.fill();
    for (let i = 0; i < 52; i++) {
      const x = noise(i + 5) * 195;
      const ridge = SEA_LEVEL - 13 - Math.max(0, 33 * Math.sin((x + 30) * 0.013) + Math.sin(x * 0.053) * 6);
      rect(x, ridge + noise(i + 55) * 11, 3 + noise(i) * 6, 2 + noise(i + 7) * 3, i % 3 ? "#537d75" : "#789878");
    }
    rect(0, SEA_LEVEL - 13, 218, 2, "#baded6");
    if (ocean.beach.id === "nazare") {
      rect(66, SEA_LEVEL - 68, 13, 21, "#e7d9b9");
      rect(64, SEA_LEVEL - 72, 17, 4, "#aa6555");
      rect(69, SEA_LEVEL - 77, 7, 5, "#344c5f");
    }
    ctx.restore();
    for (let i = 0; i < 3; i++) {
      const x = 335 + i * 11 + Math.sin(t * 0.025) * 35;
      const y = SEA_LEVEL - 92 + i % 2 * 5;
      const wing = Math.sin(t * 2 + i) > 0 ? -1 : 1;
      rect(x - 3, y + wing, 3, 1, "#6999bf");
      rect(x, y, 2, 1, "#6999bf");
      rect(x + 2, y + wing, 3, 1, "#6999bf");
    }
  }
  function drawWaterSurface(ctx, rect, ocean, t) {
    const colors = ["#279daf", "#2caabc", "#34b9c9", "#40c6d1", "#55d3d9"];
    const left = Math.floor((ocean.camera.x - 260) / 4) * 4;
    const right = left + OCEAN_WIDTH + 520;
    for (let z = 100; z > 0; z -= 10) {
      ctx.beginPath();
      let first = true;
      const vertex = (x, depth) => {
        const surface = ocean.surface(x, depth);
        if (ocean.floor(x, depth) <= surface) return;
        const p = ocean.project(x, surface, depth);
        if (first) {
          ctx.moveTo(Math.round(p.x), Math.round(p.y));
          first = false;
        } else ctx.lineTo(Math.round(p.x), Math.round(p.y));
      };
      for (let x = left; x <= right; x += 4) vertex(x, z);
      for (let x = right; x >= left; x -= 4) vertex(x, z - 10);
      ctx.closePath();
      ctx.fillStyle = colors[Math.min(4, Math.floor((100 - z) / 20))];
      ctx.fill();
      for (let x = Math.floor(left / 43) * 43; x < right; x += 43) {
        const px = x + Math.sin(t * 0.4 + x) * 3;
        if (ocean.floor(px, z) <= ocean.surface(px, z)) continue;
        const p = ocean.project(px, ocean.surface(px, z), z);
        rect(p.x, p.y, 3 + noise(x + z) * 8, 1, z > 50 ? "#61c5d0" : "#a3ece3");
      }
    }
  }
  function drawGround(ctx, rect, ocean) {
    const colors = [ocean.beach.sand, "#c29c68", "#977348", "#715b47", "#4d4b47", "#273e50"];
    for (let x = 0; x < OCEAN_WIDTH; x += 4) {
      const floor = ocean.floor(x + ocean.camera.x) - ocean.camera.y;
      for (let band = 0; band < colors.length; band++) rect(x, floor + band * 6, 4, OCEAN_HEIGHT - floor - band * 6, colors[band]);
    }
    const left = Math.floor((ocean.camera.x - 260) / 8) * 8;
    for (let z = 100; z > 0; z -= 10) {
      for (let x = left; x < left + OCEAN_WIDTH + 520; x += 8) {
        if (ocean.floor(x, z) > ocean.surface(x, z)) continue;
        ctx.beginPath();
        [[x, z], [x + 8, z], [x + 8, z - 10], [x, z - 10]].forEach(([px, depth], index) => {
          const p = ocean.project(px, ocean.floor(px, depth), depth);
          if (!index) ctx.moveTo(Math.round(p.x), Math.round(p.y));
          else ctx.lineTo(Math.round(p.x), Math.round(p.y));
        });
        ctx.closePath();
        ctx.fillStyle = z > 50 ? "#d7c198" : ocean.beach.sand;
        ctx.fill();
      }
    }
    for (let x = Math.floor(left / 37) * 37; x < left + OCEAN_WIDTH + 520; x += 37) {
      const y = ocean.floor(x);
      if (y < SEA_LEVEL + 15) {
        const p = ocean.project(x, y, 20);
        rect(p.x, p.y - 1, 2 + noise(x) * 3, 1, "#ac976d");
      } else {
        const p = ocean.project(x, y);
        rect(p.x, p.y - 2, 4 + noise(x) * 5, 2, "#398481");
        rect(p.x + 2, p.y - 5 - noise(x + 45) * 6, 2, 8, "#3e8e87");
      }
    }
  }
  function drawFish(rect, t, ocean) {
    const start = Math.floor(ocean.camera.x / 120) * 120;
    for (let x = start; x < start + OCEAN_WIDTH + 120; x += 120) {
      const px = x + Math.sin(t * 0.15 + x) * 30;
      const y = SEA_LEVEL + 27 + noise(x + 90) * 23 + Math.sin(t * 0.8 + x) * 2;
      if (ocean.floor(px) < y + 8 || ocean.surface(px) > y - 5) continue;
      const p = ocean.project(px, y);
      const size = noise(x) > 0.75 ? 2 : 1;
      rect(p.x, p.y, 5 * size, 3 * size, "#458c9a");
      rect(p.x + 2 * size, p.y, size, 3 * size, "#2c728c");
      rect(p.x - 2 * size, p.y + size, 2 * size, size, "#377c91");
    }
  }

  // app/src/components/SurfBreak/ocean.ts
  var DEPTH_SPEED_SCALE = 3.5;
  var STAND_SPEED = 28;
  var SETTLE_SPEED = 14;
  var Ocean = class {
    constructor(options = {}) {
      __publicField(this, "beach");
      __publicField(this, "conditions");
      __publicField(this, "camera", { x: 0, y: 0 });
      __publicField(this, "onFoot", false);
      __publicField(this, "recovery", 0);
      __publicField(this, "wipeoutCause", null);
      __publicField(this, "time", 0);
      __publicField(this, "x", OCEAN_WIDTH * 0.6375);
      __publicField(this, "y", SEA_LEVEL);
      __publicField(this, "z", 30);
      __publicField(this, "vx", 0);
      __publicField(this, "vy", 0);
      __publicField(this, "vz", 0);
      __publicField(this, "angle", 0);
      __publicField(this, "heading", Math.PI);
      __publicField(this, "stance", 0);
      __publicField(this, "walking", false);
      __publicField(this, "armsCrossed", false);
      __publicField(this, "posture", "prone");
      __publicField(this, "standingBlend", 0);
      __publicField(this, "paddling", false);
      __publicField(this, "speed", 0);
      __publicField(this, "waves", []);
      __publicField(this, "particles", []);
      __publicField(this, "nextWave", 3);
      __publicField(this, "waveNumber", 0);
      __publicField(this, "setNumber", 0);
      __publicField(this, "wavesInSet", 0);
      __publicField(this, "jumpCooldown", 0);
      __publicField(this, "sprayDebt", 0);
      __publicField(this, "trailDebt", 0);
      __publicField(this, "particleNumber", 0);
      __publicField(this, "jumping", false);
      __publicField(this, "slowTime", 0);
      __publicField(this, "unstableTime", 0);
      this.beach = beaches[options.beach ?? "sandbar"];
      this.conditions = { ...defaultConditions(), ...options.conditions };
      this.onFoot = options.start === "beach";
      if (this.onFoot) this.x = -80;
      this.y = this.onFoot ? this.floor(this.x, this.z) : this.surface(this.x, this.z);
      this.camera.x = this.x - OCEAN_WIDTH * 0.46;
    }
    get boardSpeed() {
      return Math.hypot(this.vx, this.vz * DEPTH_SPEED_SCALE);
    }
    get canStand() {
      return !this.onFoot && !this.recovery && this.boardSpeed >= STAND_SPEED && Math.abs(this.y - this.surface(this.x, this.z)) < 8;
    }
    floor(x, z = 0) {
      return seaFloor(x, z, this.beach);
    }
    project(x, y, z = 0) {
      return project(x, y, z, this.camera);
    }
    flow(x, z = 0, y = SEA_LEVEL) {
      return sampleWater(this.waves, this.beach, x, z, this.time, y);
    }
    surface(x, z = 0) {
      return waterSurface(this.waves, x, z, this.time);
    }
    get depth() {
      return Math.max(0, this.y - this.surface(this.x, this.z));
    }
    get barrel() {
      return this.waves.find((wave) => {
        const section = waveSection(wave, this.z);
        if (section.curl < 0.45 || section.collapse > 0.8) return false;
        const roof = barrelRoof(wave, this.x, this.z);
        return this.y > roof + 8 && this.y < this.surface(this.x, this.z) + 10;
      });
    }
    get cover() {
      return this.barrel ? ease(this.z / 65) : 0;
    }
    get state() {
      if (this.onFoot) return this.floor(this.x, this.z) > this.surface(this.x, this.z) + 2 ? "wading" : "walking";
      if (this.recovery > 0) return "recovering";
      const offset = this.y - this.surface(this.x, this.z);
      if (offset > 9) return "submerged";
      if (offset < -7) return "airborne";
      return (this.barrel || SEA_LEVEL - this.surface(this.x, this.z) > 5) && Math.hypot(this.vx, this.vz) > 8 ? "riding" : "floating";
    }
    step(dt, input) {
      if (!Number.isFinite(dt) || dt <= 0) return;
      const elapsed = Math.min(dt, 0.1);
      const steps = Math.ceil(elapsed * 120);
      for (let i = 0; i < steps; i++) this.advance(elapsed / steps, input, i === 0);
    }
    advance(dt, input, first) {
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
        if (this.posture === "standing") this.posture = "prone";
        else if (this.canStand && !input.dive) this.posture = "standing";
        this.slowTime = 0;
      }
      if (input.dive || submerged || this.recovery) this.posture = "prone";
      const standing = this.posture === "standing";
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
      if (this.z === 0 && this.vz < 0 || this.z === 100 && this.vz > 0) this.vz = 0;
      this.y += this.vy * dt;
      if (supported && !this.jumping && !input.dive && !this.recovery) {
        const nextWater = this.surface(this.x, this.z);
        if (this.y > nextWater + 2) {
          this.y = nextWater + 2;
          this.vy = Math.min(this.vy, (nextWater - previousSurface) / dt);
        }
      }
      this.slowTime = standing && touching && this.boardSpeed < SETTLE_SPEED ? this.slowTime + dt : 0;
      if (this.slowTime > 0.7) this.posture = "prone";
      this.standingBlend += ((this.posture === "standing" ? 1 : 0) - this.standingBlend) * (1 - Math.exp(-dt * 7));
      const nextStance = standing ? clamp(this.stance + (Number(input.nose) - Number(input.tail)) * dt * 0.8, -0.8, 0.9) : 0;
      this.walking = nextStance !== this.stance;
      this.stance = nextStance;
      let hazard = null;
      if (!input.dive && !submerged && !this.recovery) {
        for (const wave of this.waves) {
          const section = waveSection(wave, this.z);
          const bodyX = this.x + Math.cos(this.heading) * this.stance * 9;
          const roof = barrelRoof(wave, bodyX, this.z);
          const hitLip = this.y + 3 >= roof && this.y - (standing ? 22 : 6) <= roof + 3;
          const powerful = Math.max(wave.height, wave.amplitude) > this.beach.wipeoutHeight && section.height > 25 && wave.energy > 0.35;
          if (hitLip) {
            if (powerful && standing && wave.breaking > 0.05) {
              this.wipeOut("lip");
              break;
            }
            this.vy += (24 - this.vy) * dt * 4;
          }
          const face = (section.center - this.x) / section.frontWidth;
          const onFace = face > 0.05 && face < 1.5 && Math.abs(this.y - this.surface(this.x, this.z)) < 10;
          if (!powerful || !standing || !onFace) continue;
          if (section.collapse > 0.18 && section.collapse < 0.95) hazard = "closeout";
          else if (section.curl > 0.6 && wave.breaking > 0.05 && -this.vx < wave.speed * 0.3) hazard ?? (hazard = "stall");
        }
      }
      this.unstableTime = hazard ? this.unstableTime + dt : 0;
      if (hazard && this.unstableTime > 0.3) this.wipeOut(hazard);
      const floor = this.floor(this.x, this.z) - 9;
      if (this.y > floor) {
        this.y = floor;
        this.vy = Math.min(0, this.vy);
      }
      if (bottomDepth(this.beach, this.x, this.z) < 8 && !this.jumping) {
        this.onFoot = true;
        this.posture = "prone";
        this.recovery = 0;
      }
      if (Math.hypot(this.vx, this.vz) > 3) {
        const target = Math.atan2(-this.vz, this.vx);
        const turn = Math.atan2(Math.sin(target - this.heading), Math.cos(target - this.heading));
        this.heading += turn * (1 - Math.exp(-dt * 7));
      }
      const directionSlope = slope * Math.cos(this.heading) - depthSlope * Math.sin(this.heading);
      const targetAngle = input.dive || submerged ? clamp(-this.vy * 0.012, -0.35, 0.35) : touching ? -Math.atan(directionSlope) * 0.65 : clamp(-this.vy * 6e-3, -0.65, 0.65);
      this.angle += (targetAngle - this.angle) * (1 - Math.exp(-dt * 7));
      this.speed += (clamp(Math.abs(this.vx) / 85 + lift * 0.25, 0, 1) - this.speed) * dt * 2;
      this.advanceParticles(dt, touching, submerged || input.dive);
      this.followCamera(dt);
    }
    wipeOut(cause) {
      if (this.recovery) return;
      this.recovery = 2;
      this.wipeoutCause = cause;
      this.unstableTime = 0;
      this.posture = "prone";
      this.stance = 0;
      this.jumping = false;
      this.vy = 20;
      this.vx *= 0.65;
    }
    advanceOnFoot(dt, input) {
      const depth = bottomDepth(this.beach, this.x, this.z);
      const speed = depth > 2 ? 36 : 55;
      this.vx += ((Number(input.right) - Number(input.left)) * speed - this.vx) * (1 - Math.exp(-dt * 8));
      this.vz += ((Number(input.away) - Number(input.toward)) * 16 - this.vz) * (1 - Math.exp(-dt * 8));
      this.x += this.vx * dt;
      this.z = clamp(this.z + this.vz * dt, 0, 100);
      this.y = this.floor(this.x, this.z);
      this.vy = 0;
      this.walking = Math.hypot(this.vx, this.vz) > 3;
      this.paddling = false;
      this.stance = 0;
      if (this.walking) {
        const target = Math.atan2(-this.vz, this.vx);
        this.heading += Math.atan2(Math.sin(target - this.heading), Math.cos(target - this.heading)) * (1 - Math.exp(-dt * 9));
      }
      this.angle *= Math.exp(-dt * 8);
      this.standingBlend += (1 - this.standingBlend) * (1 - Math.exp(-dt * 7));
      if (depth > 18) {
        this.onFoot = false;
        this.walking = false;
        this.standingBlend = 0;
        this.y = this.surface(this.x, this.z);
        this.posture = "prone";
      }
    }
    followCamera(dt) {
      const targetX = this.x - OCEAN_WIDTH * 0.46 + clamp(this.vx * 0.8, -90, 90);
      this.camera.x += (targetX - this.camera.x) * (1 - Math.exp(-dt * 3));
      const targetY = Math.min(0, this.y - 170);
      this.camera.y += (targetY - this.camera.y) * (1 - Math.exp(-dt * 2));
    }
    advanceWaves(dt) {
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
        if (wave.breaking > 0.9 && wave.height < 0.8 || wave.x < -250 || wave.x + reach < this.camera.x - 1200 || wave.x - reach > Math.max(this.beach.offshore + 100, this.x + 2400)) this.waves.splice(i, 1);
      }
    }
    particle(kind, x, y, z, vx, vy) {
      if (this.particles.length >= 320) return;
      const n = noise(this.particleNumber++);
      this.particles.push({ kind, x, y, z, vx, vy, life: kind === "bubble" ? 4 : 1.3 + n * 1.4, size: n > 0.75 ? 2 : 1 });
    }
    advanceParticles(dt, touching, submerged) {
      for (const wave of this.waves) {
        if (Math.abs(wave.x - this.x) > 850) continue;
        this.sprayDebt += dt * (wave.curl + wave.breaking) * 58;
        while (this.sprayDebt >= 1) {
          this.sprayDebt--;
          const n = noise(this.particleNumber + 28);
          const z = noise(this.particleNumber + 54) * 100;
          const lip = curlPoint(wave, 1, 0, z);
          const flow = this.flow(lip.x, z, lip.y);
          this.particle("spray", lip.x + n * 9, lip.y - 3, z, flow.vx - n * 28, flow.vy - 5 - noise(this.particleNumber) * 32);
        }
      }
      this.trailDebt += dt * (submerged ? 9 : touching ? Math.abs(this.vx) * 0.36 : 0);
      while (this.trailDebt >= 1) {
        this.trailDebt--;
        const n = noise(this.particleNumber + 81);
        const tail = -Math.cos(this.heading);
        this.particle(
          submerged ? "bubble" : "spray",
          this.x + tail * 9,
          this.y + (submerged ? -2 : 0),
          this.z,
          tail * (10 + n * 25),
          submerged ? -12 : -14 - n * 24
        );
      }
      for (let i = this.particles.length - 1; i >= 0; i--) {
        const p = this.particles[i];
        p.life -= dt;
        p.x += p.vx * dt;
        if (p.kind === "spray") {
          p.vy += GRAVITY * dt;
          p.y += p.vy * dt;
          if (p.y >= this.surface(p.x, p.z) && p.vy > 0) {
            p.kind = "foam";
            p.life = 1.8;
            p.vx *= 0.3;
          }
        } else if (p.kind === "foam") {
          p.y = this.surface(p.x, p.z) + 1;
          p.vx += (this.flow(p.x, p.z, p.y).vx - p.vx) * (1 - Math.exp(-dt * 3));
        } else {
          p.y += p.vy * dt;
          p.vx *= Math.exp(-dt * 1.5);
          if (p.y < this.surface(p.x, p.z)) p.life = 0;
        }
        if (p.life <= 0 || Math.abs(p.x - this.x) > 1e3 || p.y > this.floor(p.x, p.z)) this.particles.splice(i, 1);
      }
    }
    draw(ctx, reducedMotion) {
      drawOcean(ctx, this, reducedMotion);
    }
  };

  // app/src/components/SurfBreak/barrelStudies.ts
  function createBarrelStudy(treatment) {
    const ocean = new Ocean({ beach: "point" });
    ocean.camera.x = 900;
    ocean.camera.y = -28;
    ocean.x = 1340;
    ocean.y = 294;
    ocean.z = 0;
    ocean.heading = treatment === "curtain" ? 0 : Math.PI;
    ocean.posture = "standing";
    ocean.standingBlend = 1;
    return { ocean, artwork: { surferScale: 1.8, detailedSurfer: true, barrels: [{
      treatment,
      left: 1070,
      floor: 294,
      width: 660,
      height: 105,
      roundness: treatment === "hollow" ? 1 : 0,
      curtain: treatment === "curtain" ? 1 : 0,
      lipFall: treatment === "curtain" ? 1 : 0.78,
      curlSide: "left",
      peelDirection: treatment === "curtain" ? "right" : "left",
      seed: 23
    }] } };
  }

  // app/test-harness/barrel-studies.ts
  for (const treatment of ["wall", "hollow", "curtain"]) {
    const canvas = document.querySelector(`canvas[data-treatment="${treatment}"]`);
    const status = document.querySelector(`[data-status="${treatment}"]`);
    if (!canvas || !status) throw new Error(`Missing ${treatment} study elements`);
    const context = canvas.getContext("2d");
    if (!context) {
      status.textContent = "Canvas is unavailable in this browser.";
      continue;
    }
    const { ocean, artwork } = createBarrelStudy(treatment);
    drawOcean(context, ocean, true, artwork);
    canvas.dataset.rendered = "true";
    status.textContent = "Canvas drawing, frozen frame";
  }
})();
