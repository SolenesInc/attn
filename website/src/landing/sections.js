(() => {
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const TICK_MS = 1700;
  const GLOSS_MS = 3400;

  function rng(seed) {
    return () => {
      seed = (seed * 1664525 + 1013904223) >>> 0;
      return seed / 4294967296;
    };
  }

  function field(root, isActive) {
    const holder = root.querySelector('.as-field');
    const canvas = holder && holder.querySelector('canvas');
    if (!canvas) return { wake() {}, sleep() {} };
    const ctx = canvas.getContext('2d');
    const PITCH = 22;
    const COLORS = { 1: [93, 92, 97], 2: [63, 185, 80], 3: [245, 158, 11] };
    let cols = 0, rows = 0, cells = new Uint8Array(0), focus = -1, pointer = null, frame = 0, timer = 0;
    const random = rng(7);

    function build() {
      const box = holder.getBoundingClientRect();
      const dpr = Math.min(devicePixelRatio || 1, 2);
      canvas.width = Math.round(box.width * dpr);
      canvas.height = Math.round(box.height * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      cols = Math.ceil(box.width / PITCH);
      rows = Math.ceil(box.height / PITCH);
      cells = new Uint8Array(cols * rows);
      for (let i = 0; i < cells.length; i += 1) {
        const r = random();
        cells[i] = r < 0.028 ? 3 : r < 0.09 ? 2 : r < 0.13 ? 1 : 0;
      }
      focus = nearestWaiting(cols >> 1, rows >> 1);
      draw();
    }

    function center(i) {
      return [(i % cols) * PITCH + PITCH / 2, Math.floor(i / cols) * PITCH + PITCH / 2];
    }

    function nearestWaiting(cx, cy) {
      let best = -1, bestDistance = Infinity;
      for (let i = 0; i < cells.length; i += 1) {
        if (cells[i] !== 3) continue;
        const dx = (i % cols) - cx, dy = Math.floor(i / cols) - cy;
        const distance = dx * dx + dy * dy;
        if (distance < bestDistance) { best = i; bestDistance = distance; }
      }
      return best;
    }

    function draw() {
      frame = 0;
      const width = cols * PITCH, height = rows * PITCH;
      ctx.clearRect(0, 0, width, height);
      for (let i = 0; i < cells.length; i += 1) {
        const [x, y] = center(i);
        let lift = 0;
        if (pointer) {
          const d = Math.hypot(x - pointer[0], y - pointer[1]);
          if (d < 150) lift = 1 - d / 150;
        }
        const state = cells[i];
        if (state) {
          const [r, g, b] = COLORS[state];
          ctx.fillStyle = `rgba(${r},${g},${b},${0.42 + lift * 0.5})`;
          ctx.beginPath();
          ctx.arc(x, y, 2 + lift * 1.2, 0, Math.PI * 2);
          ctx.fill();
        } else {
          ctx.fillStyle = `rgba(255,255,255,${0.055 + lift * 0.22})`;
          ctx.fillRect(x - 1, y - 1, 2, 2);
        }
      }
      if (focus >= 0) {
        const [x, y] = center(focus);
        const s = 9, l = 4;
        ctx.strokeStyle = '#ff6b35';
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) {
          ctx.moveTo(x + sx * s, y + sy * (s - l));
          ctx.lineTo(x + sx * s, y + sy * s);
          ctx.lineTo(x + sx * (s - l), y + sy * s);
        }
        ctx.stroke();
      }
    }

    function schedule() {
      if (!frame) frame = requestAnimationFrame(draw);
    }

    function tick() {
      timer = 0;
      if (!isActive()) return;
      if (focus >= 0) cells[focus] = 2;
      const pick = (from, to) => {
        for (let tries = 0; tries < 40; tries += 1) {
          const i = Math.floor(random() * cells.length);
          if (cells[i] === from) { cells[i] = to; return; }
        }
      };
      pick(2, 3);
      if (random() < 0.5) pick(1, 2);
      const fx = focus >= 0 ? focus % cols : cols >> 1;
      const fy = focus >= 0 ? Math.floor(focus / cols) : rows >> 1;
      focus = nearestWaiting(fx, fy);
      draw();
      timer = setTimeout(tick, TICK_MS);
    }

    function onPointer(event) {
      const box = holder.getBoundingClientRect();
      pointer = [event.clientX - box.left, event.clientY - box.top];
      schedule();
    }

    function onLeave() {
      pointer = null;
      schedule();
    }

    let awake = false;
    new ResizeObserver(() => { if (holder.offsetWidth) build(); }).observe(holder);

    return {
      wake() {
        if (awake || reduced.matches) return;
        awake = true;
        addEventListener('pointermove', onPointer, { passive: true });
        document.documentElement.addEventListener('pointerleave', onLeave);
        if (!timer) timer = setTimeout(tick, TICK_MS);
      },
      sleep() {
        if (!awake) return;
        awake = false;
        removeEventListener('pointermove', onPointer);
        document.documentElement.removeEventListener('pointerleave', onLeave);
        clearTimeout(timer);
        timer = 0;
        if (frame) cancelAnimationFrame(frame);
        frame = 0;
      },
    };
  }

  function init(root) {
    root.dataset.live = '';
    const sections = [...root.querySelectorAll('.as-sec')];
    let focused = null, inView = false;

    const visible = () => inView && document.visibilityState === 'visible';
    const ambient = field(root, visible);

    const videos = [...root.querySelectorAll('video.as-media')];
    const syncMotion = () => {
      for (const video of videos) video.controls = reduced.matches;
    };
    syncMotion();

    function playFocused() {
      for (const video of videos) {
        const own = video.closest('.as-sec') === focused;
        if (own && visible() && !reduced.matches) {
          video.play().catch(() => {});
        } else if (!video.paused) {
          video.pause();
        }
      }
      if (visible()) ambient.wake(); else ambient.sleep();
    }

    function setFocus(section) {
      if (section === focused) return;
      if (focused) focused.removeAttribute('data-focus');
      focused = section;
      if (section) section.dataset.focus = '';
      playFocused();
    }

    const centerLine = new IntersectionObserver((entries) => {
      for (const entry of entries) if (entry.isIntersecting) setFocus(entry.target);
    }, { rootMargin: '-46% 0px -46% 0px' });
    sections.forEach((section) => centerLine.observe(section));

    new IntersectionObserver(([entry]) => {
      inView = entry.intersectionRatio > 0;
      playFocused();
    }, { threshold: [0, 1e-6] }).observe(root);

    document.addEventListener('visibilitychange', playFocused);
    reduced.addEventListener('change', () => { syncMotion(); playFocused(); });
  }

  init(document.querySelector('[data-as-root]'));
})();
