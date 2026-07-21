# Bedrock Search pitch-video capture sheet

Capture at **1920 x 1080** (or 2560 x 1440) with the browser chrome hidden. Deliver a 1920 x 1080 H.264 MP4. The original 1280 x 720 capture is technically valid, but this framing keeps the controls readable after upload compression.

Use the revised [`../video_for_submission.srt`](../video_for_submission.srt) as the timeline. Keep the subtitle text as narration only; add the following visual callouts in the editor or capture them directly in the UI.

| Cues | Screen action | Callout treatment |
| --- | --- | --- |
| 1-2 | Branded title card, then console wide shot | Slow push from 100% to 112%; no cursor |
| 3-4 | Console header and four metric cards | Darken the rest of the screen; outline the active card with a 4 px violet stroke |
| 5-7 | Projects rail and isolation note | Arrow from `default` to `z-catalog`, then bracket both projects and spotlight the isolation note |
| 8-10 | CSV file-set card and import history | Zoom to 135%; animated arrow: file -> column -> mode -> Import file |
| 11-16 | Cold candidates, hot cache, and metric cards | Split-screen or pan across the three areas; cyan for cold, magenta for hot; animate a small data dot from cold to hot at cue 13 |
| 17-19 | API docs button, in-console docs, then Swagger | Arrow to API docs; open panel; cut to Swagger at full screen, not a tiny browser window |
| 20-22 | Application integration code or HTTP response | Use a large, full-width request/response card. Highlight `query`, returned suggestions, and the `source` field one at a time |
| 23-24 | Metrics JSON and Prometheus endpoint | Pan from latency field to cache fields; use a small moving sparkline rather than a static terminal |
| 25-26 | Docker Compose and repository tree | Use large type; reveal one service/config line and one architecture directory at a time |
| 27-30 | `docs/workflow-viewer.html?tour=1` | Full-screen architecture sequence. The viewer automatically pans, zooms, highlights nodes, and sends animated request packets along the active path |

## Capture rules

- Use one visual focus per 6-second cue. Do not leave a full console view on-screen for more than two adjacent cues.
- Crop or zoom to the active panel; avoid showing a desktop, browser tabs, address bar, or unused white space.
- Use consistent colors: violet for actions/UI, cyan for cold disk data, magenta for hot RAM data, and green for successful request flow.
- Animate arrows with a short 200-300 ms draw-on. Keep the arrow head outside the target text.
- Use a 250-400 ms ease-in/out on pans and zooms, with 8-12% motion only. Fast zooms read as accidental screen recording.
- Record UI at device scale 1 in a 16:9 1920x1080 viewport. If the console itself is captured, start at 125-135% browser zoom for detail rather than shrinking a 720p recording.

## Suggested render order

1. Record the console sections as individual 6-12 second clips, with the active item already in focus.
2. Record the Swagger, HTTP response, metrics, Docker, and source-tree sections as clean full-screen inserts.
3. Open `docs/workflow-viewer.html?tour=1` for the final architecture segment; it needs no manual dragging.
4. Assemble clips to the cue boundaries, generate narration from the SRT, then burn visual callouts only where the source clip does not already include them.
