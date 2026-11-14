#!/usr/bin/env node
// Deterministic derivatives of the supplied Twarm SVGs; no production Node dependency.
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { chromium } from 'playwright';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const assetDir = path.join(root, 'internal/admin/static');
const check = process.argv.includes('--check');
// The originals use a shortened XML namespace that standalone SVG images do
// not recognize. Normalize that declaration and source whitespace in the
// derived app assets.
// Keep the user-owned originals, geometry, and colors untouched.
const svgAsset = async name => Buffer.from((await readFile(path.join(root, name), 'utf8'))
  .replace('xmlns="http://w3.org"', 'xmlns="http://www.w3.org/2000/svg"')
  .split('\n').map(line => line.trimEnd()).join('\n').trimEnd() + '\n');
const icon = await svgAsset('twarm_logo.svg');
const wordmark = await svgAsset('twarm_logo+name.svg');
const outputs = new Map([
  [path.join(assetDir, 'twarm_logo.svg'), icon],
  [path.join(assetDir, 'twarm_logo+name.svg'), wordmark],
  // Keep existing favicon/profile URLs valid for older clients.
  [path.join(assetDir, 'webui-icon.svg'), icon],
]);

const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ deviceScaleFactor: 1, colorScheme: 'dark', reducedMotion: 'reduce' });
  for (const size of [180, 192, 512]) {
    await page.setViewportSize({ width: size, height: size });
    await page.setContent(`<style>html,body{margin:0;width:100%;height:100%;background:#0D0F12}svg{display:block;width:100%;height:100%}</style>${icon.toString('utf8')}`);
    const options = { animations: 'disabled', caret: 'hide', scale: 'css' };
    outputs.set(path.join(assetDir, `webui-icon-${size}.png`), await page.screenshot({ ...options, type: 'png' }));
    if (size === 512) {
      outputs.set(path.join(root, 'internal/gateway/assets/default-bot-profile.jpg'), await page.screenshot({ ...options, type: 'jpeg', quality: 95 }));
    }
  }
} finally {
  await browser.close();
}

for (const [filename, expected] of outputs) {
  if (check) {
    const actual = await readFile(filename);
    if (!actual.equals(expected)) throw new Error(`Brand asset differs: ${path.relative(root, filename)}`);
  } else {
    await writeFile(filename, expected);
  }
}
console.log(`Twarm brand assets ${check ? 'verified' : 'built'} (${outputs.size} files).`);
