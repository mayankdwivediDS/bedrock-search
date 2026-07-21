import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, renameSync, rmSync } from 'node:fs';
import { resolve, dirname, basename, extname } from 'node:path';
import { pathToFileURL } from 'node:url';

const [sceneArg, durationArg, outputArg] = process.argv.slice(2);
if (!sceneArg || !durationArg || !outputArg) {
  throw new Error('Usage: node render.mjs <scene.html> <seconds> <output.mp4>');
}

const scene = resolve(sceneArg);
const output = resolve(outputArg);
const durationMs = Math.round(Number(durationArg) * 1000);
if (!existsSync(scene) || !Number.isFinite(durationMs) || durationMs <= 0) {
  throw new Error('Provide an existing HTML scene and a positive duration in seconds.');
}

mkdirSync(dirname(output), { recursive: true });
const rawDir = resolve('captures', 'raw');
mkdirSync(rawDir, { recursive: true });
const chrome = process.env.CHROME_PATH || 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const browser = await chromium.launch({
  executablePath: chrome,
  headless: true,
  args: ['--hide-scrollbars', '--force-color-profile=srgb', '--autoplay-policy=no-user-gesture-required'],
});
const context = await browser.newContext({
  viewport: { width: 1920, height: 1080 },
  deviceScaleFactor: 1,
  recordVideo: { dir: rawDir, size: { width: 1920, height: 1080 } },
  colorScheme: 'dark',
});
const page = await context.newPage();
await page.goto(`${pathToFileURL(scene).href}?autoplay=1`, { waitUntil: 'load' });
await page.waitForTimeout(durationMs);
const video = page.video();
await context.close();
await browser.close();

const rawVideo = await video.path();
const rawTarget = resolve(rawDir, `${basename(output, extname(output))}.webm`);
if (existsSync(rawTarget)) rmSync(rawTarget);
renameSync(rawVideo, rawTarget);
execFileSync('ffmpeg', [
  '-hide_banner', '-loglevel', 'error', '-y', '-i', rawTarget,
  '-c:v', 'libx264', '-preset', 'medium', '-crf', '18', '-pix_fmt', 'yuv420p',
  '-movflags', '+faststart', '-an', output,
], { stdio: 'inherit' });
console.log(`Rendered ${output}`);
