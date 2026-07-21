import { createReadStream, existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { resolve } from 'node:path';

const root = process.cwd();
const source = resolve(root, 'exports', 'bedrock-search-hackathon-pitch.mp4');
const ui = resolve(root, 'caption-ui.html');
const output = 'exports\\bedrock-search-hackathon-pitch-captioned-ui.mp4';
const port = 4178;

const send = (res, code, body, type = 'text/plain') => {
  res.writeHead(code, { 'Content-Type': type });
  res.end(body);
};
const validNumber = (value, fallback, min, max) => {
  const number = Number(value);
  return Number.isFinite(number) && number >= min && number <= max ? Math.round(number) : fallback;
};

createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/') {
    createReadStream(ui).pipe(res);
    return;
  }
  if (req.method === 'GET' && req.url === '/source.mp4' && existsSync(source)) {
    res.writeHead(200, { 'Content-Type': 'video/mp4' });
    createReadStream(source).pipe(res);
    return;
  }
  if (req.method === 'POST' && req.url === '/render') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      let settings;
      try { settings = JSON.parse(body); } catch { send(res, 400, 'Invalid settings.'); return; }
      const values = {
        FrameX: validNumber(settings.frameX, 60, 0, 400),
        BottomFrame: validNumber(settings.bottomFrame, 68, 55, 400),
        CardWidth: validNumber(settings.cardWidth, 1240, 500, 1800),
        CardHeight: validNumber(settings.cardHeight, 55, 40, 160),
        FontSize: validNumber(settings.fontSize, 26, 12, 70),
        CardAlpha: validNumber(settings.cardAlpha, 82, 0, 255),
        CornerRadius: validNumber(settings.cornerRadius, 10, 0, 50),
      };
      const args = ['-ExecutionPolicy', 'Bypass', '-File', '.\\render-captioned.ps1', '-Output', output];
      for (const [key, value] of Object.entries(values)) args.push(`-${key}`, String(value));
      const job = spawn('powershell.exe', args, { cwd: root, windowsHide: true });
      let log = '';
      job.stdout.on('data', data => { log += data; });
      job.stderr.on('data', data => { log += data; });
      job.on('close', code => {
        if (code === 0) send(res, 200, JSON.stringify({ ok: true, output, log }), 'application/json');
        else send(res, 500, JSON.stringify({ ok: false, log }), 'application/json');
      });
    });
    return;
  }
  send(res, 404, 'Not found');
}).listen(port, '127.0.0.1', () => {
  console.log(`Caption UI: http://127.0.0.1:${port}`);
});
