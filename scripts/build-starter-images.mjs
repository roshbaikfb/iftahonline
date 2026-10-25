// Renders the starter templates' sample pictures (ADR-0034) from the SVG
// drawings in scripts/starter-images to JPEG, which the onboarding module
// embeds. The pictures go through the media pipeline like any upload, so
// SVG never reaches it. Run from the repository root:
//   node scripts/build-starter-images.mjs
import { readdir, readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
// Playwright is a dashboard dependency (its end-to-end tests).
const { chromium } = createRequire(join(root, 'web', 'dashboard', 'package.json'))('playwright');
const src = join(root, 'scripts', 'starter-images');
const out = join(root, 'internal', 'modules', 'onboarding', 'internal', 'templates', 'images');
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 800, height: 800 } });
for (const name of (await readdir(src)).filter((n) => n.endsWith('.svg')).sort()) {
  const svg = await readFile(join(src, name), 'utf8');
  await page.setContent(`<html><body style="margin:0">${svg}</body></html>`);
  await page.screenshot({ path: join(out, name.replace(/\.svg$/, '.jpg')), type: 'jpeg', quality: 82 });
}
await browser.close();
