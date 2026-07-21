import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const base = process.cwd();
const envFiles = [resolve(base, '..', '.env'), resolve(base, '.env'), resolve(base, '.env.local')];
for (const localEnv of envFiles) {
  if (!existsSync(localEnv)) continue;
  for (const line of readFileSync(localEnv, 'utf8').split(/\r?\n/)) {
    const match = line.match(/^\s*([A-Z0-9_]+)\s*=\s*(.*?)\s*$/);
    if (match && !process.env[match[1]]) process.env[match[1]] = match[2].replace(/^['"]|['"]$/g, '');
  }
}

const key = process.env.ELEVENLABS_API_KEY;
if (!key) throw new Error('Set ELEVENLABS_API_KEY in video-editing/.env.local.');

const audioDir = resolve(base, 'audio');
const segmentDir = resolve(audioDir, 'segments');
mkdirSync(segmentDir, { recursive: true });

const srt = readFileSync(resolve(audioDir, 'bedrock-search-final.srt'), 'utf8').replace(/\r/g, '');
const cues = srt.trim().split('\n\n').map(block => {
  const [, range, ...lines] = block.split('\n');
  const [start, end] = range.split(' --> ').map(value => {
    const [hours, minutes, seconds] = value.replace(',', '.').split(':');
    return Number(hours) * 3600 + Number(minutes) * 60 + Number(seconds);
  });
  return { start, duration: end - start, text: lines.join(' ') };
});

const headers = { 'xi-api-key': key, 'Content-Type': 'application/json', Accept: 'audio/mpeg' };
const chooseVoice = async () => {
  if (process.env.ELEVENLABS_VOICE_ID) return process.env.ELEVENLABS_VOICE_ID;
  const response = await fetch('https://api.elevenlabs.io/v1/voices', { headers: { 'xi-api-key': key } });
  if (!response.ok) throw new Error(`Could not list ElevenLabs voices (${response.status}).`);
  const { voices = [] } = await response.json();
  const selected = voices.find(voice => /sid.*indian|indian.*sid/i.test(voice.name))
    ?? voices.find(voice => /indian/i.test(`${voice.name} ${JSON.stringify(voice.labels ?? {})}`));
  if (!selected) throw new Error('No Indian voice was found. Set ELEVENLABS_VOICE_ID in .env.local.');
  console.log(`Using voice: ${selected.name}`);
  return selected.voice_id;
};
const voiceId = await chooseVoice();

const run = (command, args) => {
  const result = spawnSync(command, args, { stdio: 'inherit' });
  if (result.status !== 0) throw new Error(`${command} failed.`);
};
const durationOf = file => {
  const result = spawnSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'default=noprint_wrappers=1:nokey=1', file], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`Cannot inspect ${file}.`);
  return Number(result.stdout.trim());
};

for (const [index, cue] of cues.entries()) {
  const name = String(index + 1).padStart(2, '0');
  const raw = resolve(segmentDir, `${name}-raw.mp3`);
  const normalized = resolve(segmentDir, `${name}.m4a`);
  console.log(`Generating ${name}/${cues.length}: ${cue.text}`);
  const response = await fetch(`https://api.elevenlabs.io/v1/text-to-speech/${voiceId}?output_format=mp3_44100_128`, {
    method: 'POST', headers,
    body: JSON.stringify({
      text: cue.text,
      model_id: 'eleven_multilingual_v2',
      language_code: 'en',
      voice_settings: { stability: 0.48, similarity_boost: 0.75, style: 0.18, use_speaker_boost: true },
    }),
  });
  if (!response.ok) throw new Error(`ElevenLabs failed on cue ${name}: ${response.status} ${await response.text()}`);
  writeFileSync(raw, Buffer.from(await response.arrayBuffer()));
  const speechDuration = durationOf(raw);
  // Keep every cue on the SRT timeline. Short clips receive a natural silent gap;
  // long clips are gently accelerated before they can collide with the next cue.
  const tempo = speechDuration > cue.duration - 0.35 ? Math.min(2, speechDuration / (cue.duration - 0.35)) : 1;
  const filter = tempo > 1 ? `atempo=${tempo.toFixed(4)},apad=whole_dur=${cue.duration}` : `apad=whole_dur=${cue.duration}`;
  run('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-i', raw, '-af', filter, '-t', String(cue.duration), '-c:a', 'aac', '-b:a', '192k', normalized]);
}

const list = resolve(audioDir, 'segments.txt');
writeFileSync(list, cues.map((_, index) => `file 'segments/${String(index + 1).padStart(2, '0')}.m4a'`).join('\n') + '\n');
const voiceTrack = resolve(audioDir, 'bedrock-search-voiceover-segmented.m4a');
run('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', list, '-c:a', 'aac', '-b:a', '192k', voiceTrack]);
run('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-i', 'exports/bedrock-search-hackathon-pitch-captioned-selected.mp4', '-i', voiceTrack, '-map', '0:v:0', '-map', '1:a:0', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k', '-shortest', '-movflags', '+faststart', 'exports/bedrock-search-hackathon-pitch-final-segmented-voice.mp4']);
console.log('Created exports/bedrock-search-hackathon-pitch-final-segmented-voice.mp4');
