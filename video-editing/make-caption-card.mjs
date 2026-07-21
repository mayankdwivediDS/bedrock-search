import { readFileSync, writeFileSync } from 'node:fs';

const srt = readFileSync('audio/bedrock-search-final.srt', 'utf8').replace(/\r/g, '');
const blocks = srt.trim().split('\n\n');
const numberEnv = (name, fallback) => {
  const value = Number(process.env[name]);
  return Number.isFinite(value) ? value : fallback;
};
const config = {
  cardX: numberEnv('CAPTION_CARD_X', 340),
  cardY: numberEnv('CAPTION_CARD_Y', 1017),
  cardWidth: numberEnv('CAPTION_CARD_WIDTH', 1240),
  cardHeight: numberEnv('CAPTION_CARD_HEIGHT', 55),
  fontSize: numberEnv('CAPTION_FONT_SIZE', 26),
  textY: numberEnv('CAPTION_TEXT_Y', 1044),
  alpha: numberEnv('CAPTION_CARD_ALPHA', 82),
  cornerRadius: numberEnv('CAPTION_CORNER_RADIUS', 10),
};
const assTime = value => {
  const [h, m, rest] = value.split(':');
  const [s, ms] = rest.split(',');
  return `${Number(h)}:${m}:${s}.${ms.slice(0, 2)}`;
};
const wrap = text => {
  if (text.length <= 78) return text;
  const split = text.lastIndexOf(' ', 78);
  return `${text.slice(0, split)}\\N${text.slice(split + 1)}`;
};
const header = `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding
Style: Caption,Arial,${config.fontSize},&H00FFFFFF,&H000000FF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,0,0,2,0,0,0,1
Style: Card,Arial,1,&H00000000,&H00000000,&H50000000,&H50000000,0,0,0,0,100,100,0,0,1,0,0,7,0,0,0,1

[Events]
Format: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text
`;
// The caption lives in the external lower letterbox, never over the recorded UI.
const alphaHex = Math.max(0, Math.min(255, config.alpha)).toString(16).padStart(2, '0').toUpperCase();
const right = config.cardWidth - config.cornerRadius;
const bottom = config.cardHeight - config.cornerRadius;
const roundedCard = `{\\an7\\pos(${config.cardX},${config.cardY})\\p1\\1c&H000000&\\1a&H${alphaHex}&}m ${config.cornerRadius} 0 l ${right} 0 b ${right + 6} 0 ${config.cardWidth} 4 ${config.cardWidth} ${config.cornerRadius} l ${config.cardWidth} ${bottom} b ${config.cardWidth} ${bottom + 6} ${right + 6} ${config.cardHeight} ${right} ${config.cardHeight} l ${config.cornerRadius} ${config.cardHeight} b 4 ${config.cardHeight} 0 ${bottom + 6} 0 ${bottom} l 0 ${config.cornerRadius} b 0 4 4 0 ${config.cornerRadius} 0`;
const events = blocks.map(block => {
  const [, range, ...lines] = block.split('\n');
  const [from, to] = range.split(' --> ');
  const text = wrap(lines.join(' ').replace(/[{}]/g, ''));
  return [
    `Dialogue: 0,${assTime(from)},${assTime(to)},Card,,0,0,0,,${roundedCard}`,
    `Dialogue: 1,${assTime(from)},${assTime(to)},Caption,,0,0,0,,{\\an2\\pos(960,${config.textY})}${text}`,
  ].join('\n');
}).join('\n');
writeFileSync('audio/bedrock-search-caption-card.ass', header + events + '\n', 'utf8');
