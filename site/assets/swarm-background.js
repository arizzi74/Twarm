'use strict';
(() => {
  const canvas = document.getElementById('swarm-background');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;

  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const mouseAvailable = window.matchMedia('(any-hover: hover) and (any-pointer: fine)');
  const pointer = { x: 0, y: 0, active: false };
  const idleColor = [45, 50, 54];
  const activeColor = [106, 138, 56]; // A muted version of the logo's lime.
  const vertices = [[0, -16], [-14, 8], [14, 8]];
  let arrows = [];
  let width = 0, height = 0, frame = 0, previousTime = 0;

  // Stable placement avoids a new random pattern after every resize.
  function noise(column, row, salt) {
    const value = Math.sin(column * 127.1 + row * 311.7 + salt * 74.7) * 43758.5453;
    return value - Math.floor(value);
  }

  function paint() {
    ctx.clearRect(0, 0, width + 1, height + 1);
    for (const arrow of arrows) {
      const sine = Math.sin(arrow.angle), cosine = Math.cos(arrow.angle);
      if (arrow.glow < 0.002) ctx.fillStyle = 'rgba(45,50,54,0.34)';
      else {
        const color = idleColor.map((channel, i) => Math.round(channel + (activeColor[i] - channel) * arrow.glow));
        ctx.fillStyle = `rgba(${color.join(',')},${0.34 + arrow.glow * 0.34})`;
      }
      ctx.beginPath();
      // The same 28:24 triangle as the delta in the Twarm logo, about its centroid.
      for (let i = 0; i < vertices.length; i++) {
        const point = vertices[i];
        const x = point[0] * arrow.size / 28, y = point[1] * arrow.size / 28;
        const px = arrow.x + arrow.offsetX + x * cosine - y * sine;
        const py = arrow.y + arrow.offsetY + x * sine + y * cosine;
        if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
      }
      ctx.closePath();
      ctx.fill();
    }
  }

  function animate(time) {
    frame = 0;
    if (document.hidden) return;
    const elapsed = previousTime ? Math.min(time - previousTime, 50) : 16;
    previousTime = time;
    const interactive = pointer.active && !reducedMotion.matches && mouseAvailable.matches;
    const radius = Math.min(220, Math.max(150, width * 0.15));
    let unsettled = false;
    for (const arrow of arrows) {
      const dx = pointer.x - arrow.x, dy = pointer.y - arrow.y;
      const proximity = interactive ? Math.max(0, 1 - Math.hypot(dx, dy) / radius) : 0;
      // Anchored springs resist the pull: even at full influence arrows move
      // less than ten pixels, then gently rebound to their original positions.
      const targetX = dx * proximity * 0.18, targetY = dy * proximity * 0.18;
      let remaining = elapsed / 1000;
      while (remaining > 0) {
        const step = Math.min(remaining, 1 / 120);
        arrow.velocityX += ((targetX - arrow.offsetX) * 130 - arrow.velocityX * 14) * step;
        arrow.velocityY += ((targetY - arrow.offsetY) * 130 - arrow.velocityY * 14) * step;
        arrow.offsetX += arrow.velocityX * step;
        arrow.offsetY += arrow.velocityY * step;
        remaining -= step;
      }
      if (Math.hypot(targetX - arrow.offsetX, targetY - arrow.offsetY) > 0.015 ||
          Math.hypot(arrow.velocityX, arrow.velocityY) > 0.03) unsettled = true;
      else {
        arrow.offsetX = targetX; arrow.offsetY = targetY;
        arrow.velocityX = arrow.velocityY = 0;
      }
      const glow = proximity * proximity;
      const angle = proximity > 0 ? Math.atan2(dy - arrow.offsetY, dx - arrow.offsetX) + Math.PI / 2 : arrow.restAngle;
      const delta = Math.atan2(Math.sin(angle - arrow.angle), Math.cos(angle - arrow.angle));
      if (Math.abs(delta) > 0.003) {
        arrow.angle += delta * (1 - Math.exp(-elapsed / 145));
        unsettled = true;
      } else arrow.angle = angle;
      if (Math.abs(glow - arrow.glow) > 0.002) {
        arrow.glow += (glow - arrow.glow) * (1 - Math.exp(-elapsed / (glow > arrow.glow ? 90 : 260)));
        unsettled = true;
      } else arrow.glow = glow;
    }
    paint();
    // No perpetual animation loop: stop as soon as the mouse wake settles.
    if (unsettled) frame = requestAnimationFrame(animate);
    else previousTime = 0;
  }

  function wake() {
    if (!frame && !document.hidden) frame = requestAnimationFrame(animate);
  }

  function reset() {
    pointer.active = false;
    if (frame) cancelAnimationFrame(frame);
    frame = previousTime = 0;
    for (const arrow of arrows) {
      arrow.angle = arrow.restAngle; arrow.glow = 0;
      arrow.offsetX = arrow.offsetY = arrow.velocityX = arrow.velocityY = 0;
    }
    if (!document.hidden) paint();
  }

  function resize() {
    width = document.documentElement.clientWidth || window.innerWidth;
    height = window.innerHeight;
    // Keep the decorative layer inexpensive on high-density and large displays.
    const scale = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(4000000 / Math.max(1, width * height)));
    canvas.width = Math.max(1, Math.round(width * scale));
    canvas.height = Math.max(1, Math.round(height * scale));
    ctx.setTransform(scale, 0, 0, scale, 0, 0);
    const spacing = Math.max(36, Math.sqrt(width * height / 1600));
    arrows = [];
    for (let row = 0; row < Math.ceil(height / spacing); row++) {
      for (let column = 0; column < Math.ceil(width / spacing); column++) {
        const angle = (noise(column, row, 3) - 0.5) * Math.PI * 2;
        arrows.push({
          x: (column + 0.5 + (noise(column, row, 1) - 0.5) * 0.45) * spacing,
          y: (row + 0.5 + (noise(column, row, 2) - 0.5) * 0.45) * spacing,
          size: 6 + noise(column, row, 4) * 4,
          restAngle: angle, angle, glow: 0,
          offsetX: 0, offsetY: 0, velocityX: 0, velocityY: 0,
        });
      }
    }
    reset();
  }

  window.addEventListener('pointermove', event => {
    if (event.pointerType !== 'mouse' || reducedMotion.matches || !mouseAvailable.matches) return;
    pointer.x = event.clientX;
    pointer.y = event.clientY;
    pointer.active = true;
    wake();
  }, { passive: true });
  function leave() { pointer.active = false; wake(); }
  document.documentElement.addEventListener('pointerleave', leave, { passive: true });
  window.addEventListener('blur', leave);
  window.addEventListener('resize', resize, { passive: true });
  document.addEventListener('visibilitychange', reset);
  reducedMotion.addEventListener('change', reset);
  mouseAvailable.addEventListener('change', reset);
  resize();
})();
