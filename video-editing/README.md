# Bedrock Search video-production workspace

This directory contains the reusable pitch-video scenes and rendering controls. Local narration, captured clips, dependencies, and final exports remain gitignored.

## Structure

- `segments/01-problem/` — automated HD opening scene: the search/autocomplete problem and Bedrock Search's value.
- `captures/` — screen recordings of the real console, Swagger, and codebase.
- `audio/` — generated voiceover files.
- `exports/` — intermediate and final MP4s.

## First segment

Open `segments/01-problem/index.html` in a clean browser window at **1920 x 1080**. It plays automatically for 30 seconds, loops, and contains no external assets or network dependencies.

Useful controls:

- `R` — restart from the first frame.
- `?autoplay=0` — open without starting the animation.

For capture, use a 1920 x 1080 browser viewport, hide browser chrome, record at 30 fps, and trim exactly from 00:00 to 00:30. The scene intentionally has no voiceover baked in; align it to the opening cues in the SRT during editing.

## Automated render

After the first local setup, run this from `video-editing`:

```powershell
npm install
npm run render:problem
```

The renderer records the HTML with Chrome at 1920 x 1080 and creates `exports/01-problem.mp4`. Raw WebM captures remain in `captures/raw/` for troubleshooting.

## Caption renderer UI

Start the local caption-layout UI with:

```powershell
npm run caption:ui
```

Open `http://127.0.0.1:4178`, adjust the outer frame and caption controls, then select **Render MP4**. The renderer produces a local MP4 in `exports/`; it never adds media files to Git.
