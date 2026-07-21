import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { mkdirSync, renameSync, rmSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

const root = 'https://github.com/mayankdwivediDS/bedrock-search';
const raw = resolve('captures', 'raw');
const output = resolve('exports', '06-github-readme.mp4');
mkdirSync(raw, { recursive: true });
mkdirSync(resolve('exports'), { recursive: true });

const browser = await chromium.launch({
  executablePath: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  headless: true,
  args: ['--hide-scrollbars', '--force-color-profile=srgb'],
});
const context = await browser.newContext({
  viewport: { width: 1920, height: 1080 },
  recordVideo: { dir: raw, size: { width: 1920, height: 1080 } },
});
const page = await context.newPage();
await page.goto(root, { waitUntil: 'networkidle', timeout: 60000 });
await page.waitForTimeout(3500);
await page.evaluate(() => window.scrollTo({ top: Math.round(document.body.scrollHeight * 0.52), behavior: 'smooth' }));
await page.waitForTimeout(3000);
await page.evaluate(() => window.scrollTo({ top: Math.round(document.body.scrollHeight * 0.68), behavior: 'smooth' }));
await page.waitForTimeout(9000);
await page.mouse.wheel(0, 450);
await page.waitForTimeout(3500);
const video = page.video();
await context.close();
await browser.close();
const source = await video.path();
const rawTarget = resolve(raw, '06-github-readme.webm');
if (existsSync(rawTarget)) rmSync(rawTarget);
renameSync(source, rawTarget);
execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-i', rawTarget, '-t', '20', '-c:v', 'libx264', '-preset', 'medium', '-crf', '18', '-pix_fmt', 'yuv420p', '-an', output], { stdio: 'inherit' });
console.log(`Captured ${output}`);
