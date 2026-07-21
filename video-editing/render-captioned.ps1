param(
  [string]$Source = 'exports\bedrock-search-hackathon-pitch.mp4',
  [string]$Output = 'exports\bedrock-search-hackathon-pitch-captioned-custom.mp4',
  [int]$FrameX = 60,
  [int]$BottomFrame = 68,
  [int]$CardWidth = 1240,
  [int]$CardHeight = 55,
  [int]$CardX = -1,
  [int]$CardY = -1,
  [int]$FontSize = 26,
  [int]$TextY = -1,
  [int]$CardAlpha = 82,
  [int]$CornerRadius = 10
)

$ErrorActionPreference = 'Stop'

# The video is placed at the top; captions are drawn only in the lower outer frame.
$videoWidth = 1920 - (2 * $FrameX)
$videoHeight = 1080 - $BottomFrame
if ($videoWidth -le 0 -or $videoHeight -le 0) { throw 'Frame values leave no space for the video.' }

if ($CardX -lt 0) { $CardX = [math]::Floor((1920 - $CardWidth) / 2) }
if ($CardY -lt 0) { $CardY = $videoHeight + [math]::Floor(($BottomFrame - $CardHeight) / 2) }
if ($TextY -lt 0) { $TextY = $CardY + [math]::Floor($CardHeight / 2) }
if ($CardY -lt $videoHeight) { throw 'CardY must be inside the bottom frame, not over the video.' }

$env:CAPTION_CARD_X = $CardX
$env:CAPTION_CARD_Y = $CardY
$env:CAPTION_CARD_WIDTH = $CardWidth
$env:CAPTION_CARD_HEIGHT = $CardHeight
$env:CAPTION_FONT_SIZE = $FontSize
$env:CAPTION_TEXT_Y = $TextY
$env:CAPTION_CARD_ALPHA = $CardAlpha
$env:CAPTION_CORNER_RADIUS = $CornerRadius

npm run make:caption-card
if ($LASTEXITCODE -ne 0) { throw 'Subtitle-card generation failed.' }

$filter = "scale=${videoWidth}:${videoHeight}:force_original_aspect_ratio=decrease,pad=1920:1080:${FrameX}:0:color=0x111111,ass=audio/bedrock-search-caption-card.ass"
ffmpeg -hide_banner -loglevel error -y -i $Source -vf $filter -c:v libx264 -preset medium -crf 18 -pix_fmt yuv420p -c:a copy -movflags +faststart $Output
if ($LASTEXITCODE -ne 0) { throw 'FFmpeg rendering failed.' }

Write-Host "Created $Output"
