# Terminal visual assets

All primary presentation assets use a high-resolution terminal visual language. Each SVG has a native 3840 × 2160 canvas, and its matching PNG is a rendered 4K export.

| Asset | Use in the demo |
| --- | --- |
| `terminal-hot-cold.svg` / `.png` | Explain the cold disk index, hot in-memory prefix cache, and promotion path. |
| `terminal-project-isolation.svg` / `.png` | Explain multiple independent search projects managed from one console. |
| `terminal-api-observability.svg` / `.png` | Explain the Go API, Swagger/OpenAPI, metrics, health checks, and Docker delivery. |
| `../architecture-flow.svg` | Direct Mermaid SVG render, sized for a clear full-screen video workflow section. |
| `../workflow.mmd` | Editable Mermaid source for the complete request-to-search flow. |
| `../workflow-viewer.html` | Interactive Mermaid presentation: drag to pan, use the mouse wheel or controls to zoom. |

Open `workflow-viewer.html` in a browser for the interactive flowchart. It loads Mermaid from jsDelivr, so the first render requires an internet connection.
