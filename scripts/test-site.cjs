#!/usr/bin/env node
'use strict';
// Static Pages checks: no production/build dependency, and no external requests.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const site = path.resolve(__dirname, '../site');
const repo = path.resolve(__dirname, '..');
const prefix = '/Twarm/';
const screenshots = process.env.SITE_SCREENSHOTS || '/tmp/telegramgw-site-preview';
const mime = { '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.png': 'image/png', '.svg': 'image/svg+xml', '.json': 'application/json' };
const server = http.createServer((request, response) => {
  const url = new URL(request.url, 'http://localhost');
  if (!url.pathname.startsWith(prefix)) { response.writeHead(404); response.end(); return; }
  const relative = decodeURIComponent(url.pathname.slice(prefix.length)) || 'index.html';
  const file = path.resolve(site, relative);
  if (!file.startsWith(site + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) { response.writeHead(404); response.end(); return; }
  response.writeHead(200, { 'Content-Type': mime[path.extname(file)] || 'text/plain' });
  response.end(fs.readFileSync(file));
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  assert.equal((await fetch(origin + prefix)).status, 200, 'The local preview serves the repository Pages entry point');
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ colorScheme: 'dark', permissions: ['clipboard-read', 'clipboard-write'], reducedMotion: 'reduce' });
    const page = await context.newPage();
    const errors = [], failed = [], external = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('response', response => { if (response.status() >= 400) failed.push(response.url()); });
    await page.route('**/*', route => { if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); return route.abort('blockedbyclient'); } return route.continue(); });
    fs.mkdirSync(screenshots, { recursive: true });
    for (const width of [1440, 1024, 768, 390, 320]) {
      await page.setViewportSize({ width, height: width <= 390 ? 844 : 1000 });
      await page.goto(origin + prefix);
      await page.locator('h1').waitFor();
      assert.equal(await page.title(), 'Twarm — Coding agent swarm control hub');
      assert.equal(await page.locator('meta[property="og:title"]').getAttribute('content'), 'Twarm — Coding agent swarm control hub');
      assert.equal(await page.locator('meta[property="og:url"]').getAttribute('content'), 'https://arizzi74.github.io/Twarm/');
      assert.equal(await page.locator('meta[property="og:image"]').getAttribute('content'), 'https://arizzi74.github.io/Twarm/assets/screenshots/webui-desktop.png');
      assert.match(await page.locator('h1').innerText(), /Coding agent\s+swarm control hub/);
      assert.match(await page.locator('.hero-description').textContent(), /^Boost your development workflow with a single control hub for coding agents\./);
      assert.match(await page.locator('.hero-description').textContent(), /manage multiple coding agent installations across servers/i);
      assert.match(await page.locator('.hero-description').textContent(), /Codex and Claude Code are supported today through dedicated workers/);
      assert.equal(await page.locator('.release-pill').getAttribute('href'), 'https://github.com/arizzi74/Twarm/releases/tag/v0.7.33');
      assert.match(await page.locator('meta[name="description"]').getAttribute('content'), /Twarm 0\.7\.33.*Codex and Claude Code/);
      assert.match(await page.locator('#agents').textContent(), /Claude follow-ups are queued, not steered into the active turn/);
      assert.match(await page.locator('#agents').textContent(), /terminal attachment and project archive\/restore remain Codex-only/);
      assert.equal(await page.getByRole('link', { name: 'Twarm · Coding agent swarm control hub home', exact: true }).count(), 1);
      assert.equal(await page.locator('.site-header .brand-wordmark').getAttribute('src'), 'assets/twarm_logo+name.svg');
      assert.equal(await page.locator('.site-header .brand-wordmark').getAttribute('alt'), 'Twarm');
      assert.equal(await page.locator('.gateway-node > strong').textContent(), 'Twarm Gateway');
      assert.equal(await page.locator('.worker-node > strong').textContent(), 'Twarm Worker');
      await page.evaluate(async () => { for (const image of document.images) image.loading = 'eager'; await Promise.all([...document.images].map(image => image.decode())); });
      const layout = await page.evaluate(() => ({ width: innerWidth, pageWidth: document.documentElement.scrollWidth, bodyWidth: document.body.scrollWidth, images: [...document.images].map(image => ({ alt: image.alt, brand: image.matches('.brand-wordmark,.brand-icon'), loaded: image.naturalWidth > 0, width: image.clientWidth, height: image.clientHeight, ratio: image.naturalWidth / image.naturalHeight })) }));
      assert.ok(layout.pageWidth <= width + 1 && layout.bodyWidth <= width + 1, `Horizontal overflow at ${width}: ${JSON.stringify(layout)}`);
      for (const image of layout.images) { assert.ok(image.loaded, 'Image failed to load'); if (!image.brand) assert.ok(image.alt.length > 20, 'Screenshot description missing'); assert.ok(Math.abs(image.width / image.height / image.ratio - 1) < .02, 'Image aspect ratio distorted: ' + JSON.stringify(image)); }
      assert.equal(await page.locator('h1').count(), 1);
      assert.equal(await page.locator('#screenshots').count(), 1);
      if (width === 1440 || width === 390) {
        await page.screenshot({ path: path.join(screenshots, `site-${width}.png`) });
        await page.screenshot({ path: path.join(screenshots, `site-${width}-full.png`), fullPage: true });
      }
    }
    // Every local URL must work under a repository Pages prefix, not just '/'.
    const links = await page.locator('a[href],link[href],script[src],img[src]').evaluateAll(nodes => nodes.map(node => node.getAttribute('href') || node.getAttribute('src')));
    for (const link of links) {
      if (link.startsWith('#')) { if (link.length > 1) assert.equal(await page.locator('[id="' + link.slice(1) + '"]').count(), 1, 'Broken anchor ' + link); continue; }
      if (/^https:\/\//.test(link)) {
        assert.ok(link.startsWith('https://github.com/arizzi74/Twarm') || link === 'https://code.claude.com/docs/en/setup' || link === 'https://arizzi74.github.io/Twarm/', 'Unexpected outbound link: ' + link);
        const local = link.split('/blob/main/')[1] || link.split('/tree/main/')[1];
        if (local) assert.ok(fs.existsSync(path.join(repo, local.split('#')[0])), 'Broken repository documentation link: ' + link);
        continue;
      }
      assert.ok(!link.startsWith('/'), 'Asset URL breaks repository Pages prefix: ' + link);
      assert.ok(fs.existsSync(path.join(site, link)), 'Missing site asset: ' + link);
    }
    for (const [name, label] of [['gateway', 'gateway'], ['worker', 'worker'], ['claude-worker', 'Claude Code worker']]) {
      const button = page.getByRole('button', { name: `Copy ${label} install command`, exact: true });
      await button.click();
      const expected = await page.locator('#' + name + '-command').textContent();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), expected.trim());
      assert.equal(await button.getAttribute('data-copied'), 'true');
    }
    assert.equal(await page.locator('#gateway-command').innerText(), 'curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sudo sh -s -- setup gateway');
    assert.equal(await page.locator('#worker-command').innerText(), 'curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sh -s -- setup worker');
    assert.equal(await page.locator('#claude-worker-command').innerText(), 'curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sh -s -- setup claude-worker');
    assert.match(await page.locator('#install').textContent(), /updates supported native Claude CLI installations through their configured official release channel when idle/);
    assert.match(await page.locator('#install').textContent(), /optional authenticated-first procedure/);
    assert.doesNotMatch(await page.locator('#install').textContent(), /MACT_INSTALLER_DIR|TWARM_INSTALLER_DIR|Requires a preinstalled trusted GitHub CLI/);
    assert.match(await page.locator('#install').textContent(), /checksums/);
    await page.evaluate(() => Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: () => Promise.reject(new Error('Clipboard unavailable')) }));
    await page.getByRole('button', { name: 'Copy worker install command', exact: true }).click();
    assert.match(await page.locator('#copy-status').innerText(), /command is selected/);
    assert.equal(await page.evaluate(() => getSelection().toString()), (await page.locator('#worker-command').textContent()).trim());
    await page.goto(origin + prefix);
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement.className), 'skip-link');
    await page.keyboard.press('Enter');
    assert.equal(new URL(page.url()).hash, '#main');
    // Exercise the actual Canvas pixels and geometry, rather than only checking CSS.
    const motionPage = await context.newPage();
    motionPage.on('pageerror', error => errors.push(error.message));
    await motionPage.route('**/*', route => {
      if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); return route.abort('blockedbyclient'); }
      return route.continue();
    });
    await motionPage.addInitScript(() => {
      window.swarmTestFrames = 0;
      window.swarmTestHidden = false;
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.swarmTestHidden });
      const requestFrame = window.requestAnimationFrame.bind(window);
      window.requestAnimationFrame = callback => { window.swarmTestFrames++; return requestFrame(callback); };
      // Observe the actual drawn triangles without exposing animation internals.
      window.swarmTestTriangles = [];
      window.swarmTestTrack = [];
      const prototype = CanvasRenderingContext2D.prototype;
      const paths = new WeakMap();
      for (const method of ['clearRect', 'moveTo', 'lineTo', 'fill']) {
        const original = prototype[method];
        prototype[method] = function (...args) {
          if (this.canvas.id === 'swarm-background') {
            if (method === 'clearRect') window.swarmTestTriangles = [];
            if (method === 'moveTo') paths.set(this, [{ x: args[0], y: args[1] }]);
            if (method === 'lineTo') paths.get(this)?.push({ x: args[0], y: args[1] });
            if (method === 'fill' && paths.get(this)?.length === 3) {
              const points = paths.get(this);
              const triangle = {
                x: points.reduce((sum, p) => sum + p.x, 0) / 3,
                y: points.reduce((sum, p) => sum + p.y, 0) / 3,
                tip: points[0],
              };
              if (window.swarmTestTriangles.length === window.swarmTestTrackIndex) window.swarmTestTrack.push(triangle);
              window.swarmTestTriangles.push(triangle);
            }
          }
          return original.apply(this, args);
        };
      }
    });
    await motionPage.emulateMedia({ reducedMotion: 'no-preference' });
    await motionPage.setViewportSize({ width: 1100, height: 760 });
    await motionPage.goto(origin + prefix);
    await motionPage.waitForFunction(() => document.querySelector('#swarm-background').width === innerWidth);
    const pixels = () => motionPage.evaluate(() => {
      const canvas = document.querySelector('#swarm-background'), ctx = canvas.getContext('2d');
      const { width, height } = canvas;
      const data = ctx.getImageData(0, 0, width, height).data;
      let green = 0, peak = 0, alphaHash = 2166136261, cornerHash = 2166136261;
      for (let i = 0; i < data.length; i += 4) {
        if (data[i + 3] > 30 && data[i + 1] > data[i] + 8 && data[i + 1] > data[i + 2] + 15) green++;
        if (data[i + 3] > 30) peak = Math.max(peak, data[i + 1]);
        if (i % 16 === 0) alphaHash = Math.imul(alphaHash ^ data[i + 3], 16777619);
        if ((i / 4) % width < 80 && Math.floor(i / 4 / width) < 80) cornerHash = Math.imul(cornerHash ^ data[i] ^ data[i + 3], 16777619);
      }
      return { png: canvas.toDataURL(), green, peak, alphaHash, cornerHash, frames: window.swarmTestFrames, triangles: window.swarmTestTriangles };
    });
    const settle = () => motionPage.waitForFunction(() => {
      const now = performance.now();
      if (window.swarmTestObservedFrames !== window.swarmTestFrames) {
        window.swarmTestObservedFrames = window.swarmTestFrames;
        window.swarmTestLastChange = now;
      }
      return now - (window.swarmTestLastChange || now) > 150;
    }, null, { polling: 80, timeout: 5000 });
    await motionPage.evaluate(() => window.dispatchEvent(new Event('blur')));
    await settle();
    const idle = await pixels();
    assert.equal(idle.green, 0, 'Resting arrows are gray');
    assert.ok(idle.triangles.length > 400 && idle.triangles.length < 900, 'The field retains the previous lighter density');
    const trackedIndex = idle.triangles.findIndex(p => Math.hypot(550 - p.x, 280 - p.y) > 60 && Math.hypot(550 - p.x, 280 - p.y) < 100);
    assert.ok(trackedIndex >= 0, 'A nearby arrow is available to observe');
    await motionPage.evaluate(index => { window.swarmTestTrackIndex = index; }, trackedIndex);
    assert.equal(await motionPage.locator('#swarm-background').getAttribute('aria-hidden'), 'true');
    assert.equal(await motionPage.locator('#swarm-background').evaluate(canvas => getComputedStyle(canvas).pointerEvents), 'none');
    await motionPage.screenshot({ path: path.join(screenshots, 'swarm-idle.png') });
    await motionPage.mouse.move(550, 280);
    await motionPage.waitForFunction(() => window.swarmTestFrames > 10, null, { polling: 40 });
    await settle();
    const active = await pixels();
    assert.ok(active.green > 20, 'Nearby arrows fade toward lime under the real mouse');
    assert.ok(active.peak < 170, 'The glow is subdued compared with the logo green channel (245)');
    assert.notEqual(active.alphaHash, idle.alphaHash, 'The triangle geometry rotates, not just its color');
    assert.equal(active.cornerHash, idle.cornerHash, 'Distant arrows are unaffected by the mouse');
    const home = idle.triangles[trackedIndex], pulled = active.triangles[trackedIndex];
    const movement = Math.hypot(pulled.x - home.x, pulled.y - home.y);
    assert.ok(movement > 2 && movement < 12, 'Nearby arrows are gently pulled a bounded distance');
    assert.ok(Math.hypot(550 - pulled.x, 280 - pulled.y) < Math.hypot(550 - home.x, 280 - home.y) - 2, 'The pull moves the arrow closer to the mouse');
    const tipX = pulled.tip.x - pulled.x, tipY = pulled.tip.y - pulled.y;
    const mouseX = 550 - pulled.x, mouseY = 280 - pulled.y;
    assert.ok((tipX * mouseX + tipY * mouseY) / Math.hypot(tipX, tipY) / Math.hypot(mouseX, mouseY) > 0.995, 'The arrow points toward the actual mouse position');
    await motionPage.screenshot({ path: path.join(screenshots, 'swarm-active.png') });
    await motionPage.evaluate(() => { window.swarmTestTrack = []; });
    await motionPage.mouse.move(-10, -10);
    await settle();
    const returnPath = await motionPage.evaluate(() => window.swarmTestTrack);
    const rebound = returnPath.map(p => ((p.x - home.x) * (pulled.x - home.x) + (p.y - home.y) * (pulled.y - home.y)) / movement);
    assert.ok(rebound.some(distance => distance < -0.03), 'The return has a small elastic rebound before settling');
    assert.equal((await pixels()).png, idle.png, 'The field settles back to exactly its resting appearance');
    const stopped = await pixels();
    await motionPage.waitForTimeout(180);
    assert.equal((await pixels()).frames, stopped.frames, 'No animation frames are requested while idle');
    await motionPage.mouse.move(500, 250);
    await motionPage.emulateMedia({ reducedMotion: 'reduce' });
    await motionPage.mouse.move(650, 320);
    await settle();
    assert.equal((await pixels()).png, idle.png, 'Reduced motion retains a static gray field');
    await motionPage.emulateMedia({ reducedMotion: 'no-preference' });
    await motionPage.mouse.move(520, 270);
    await motionPage.evaluate(() => { window.swarmTestHidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    const hiddenFrames = (await pixels()).frames;
    await motionPage.waitForTimeout(180);
    assert.equal((await pixels()).frames, hiddenFrames, 'A hidden tab stops requesting frames');
    await motionPage.evaluate(() => { window.swarmTestHidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    assert.equal((await pixels()).png, idle.png, 'A restored tab starts from the quiet background');
    await motionPage.evaluate(() => Object.defineProperty(window, 'devicePixelRatio', { configurable: true, value: 4 }));
    await motionPage.setViewportSize({ width: 1440, height: 1000 });
    await motionPage.waitForFunction(() => document.querySelector('#swarm-background').getBoundingClientRect().width === innerWidth);
    const highDensity = await motionPage.locator('#swarm-background').evaluate(canvas => ({ pixels: canvas.width * canvas.height, width: canvas.getBoundingClientRect().width, height: canvas.getBoundingClientRect().height }));
    assert.ok(highDensity.pixels <= 4010000, 'High-density displays have a bounded Canvas buffer');
    assert.equal(highDensity.width, 1440); assert.equal(highDensity.height, 1000);
    await motionPage.close();
    const touchContext = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true, reducedMotion: 'no-preference' });
    try {
      const touchPage = await touchContext.newPage();
      touchPage.on('pageerror', error => errors.push(error.message));
      await touchPage.route('**/*', route => {
        if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); return route.abort('blockedbyclient'); }
        return route.continue();
      });
      await touchPage.goto(origin + prefix);
      await touchPage.waitForFunction(() => document.querySelector('#swarm-background').width > 0);
      const beforeTouch = await touchPage.locator('#swarm-background').evaluate(canvas => canvas.toDataURL());
      await touchPage.touchscreen.tap(5, 5);
      await touchPage.evaluate(() => window.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'touch', clientX: 190, clientY: 250 })));
      await touchPage.waitForTimeout(180);
      assert.equal(await touchPage.locator('#swarm-background').evaluate(canvas => canvas.toDataURL()), beforeTouch, 'Touch interaction does not animate the background');
      await touchPage.evaluate(() => scrollTo(0, 500));
      assert.equal(await touchPage.locator('#swarm-background').evaluate(canvas => canvas.getBoundingClientRect().top), 0, 'The decoration stays behind the viewport during scroll');
    } finally { await touchContext.close(); }

    assert.deepEqual(errors, []); assert.deepEqual(failed, []); assert.deepEqual(external, []);
    const capture = JSON.parse(fs.readFileSync(path.join(site, 'assets/screenshots/capture.json'), 'utf8'));
    assert.equal(capture.demo_data, true); assert.ok(!Number.isNaN(Date.parse(capture.captured_at)));
    assert.equal(capture.product, 'Twarm'); assert.equal(capture.release, '0.7.33');
    console.log('Website passed: 320/390/768/1024/1440 px, real screenshot assets, repository-prefix URLs, documentation links, clipboard success/fallback, keyboard navigation, mouse-driven swarm rotation/fading and elastic attraction, reduced motion, touch/hidden/idle behavior, and zero external requests.');
    console.log('Page previews: ' + screenshots);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => server.close());
