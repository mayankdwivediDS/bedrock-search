# Bedrock Search

> Fast, observable autocomplete with an adaptive hot trie cache and a versioned, disk-backed corpus.

Bedrock Search is a Go service for search-as-you-type. It serves matching terms from an in-memory prefix trie when a prefix is popular, falls back to an indexed sorted corpus on disk when it is not, and promotes repeated demand automatically.

It includes an operations console, isolated search projects, versioned CSV ingestion, metrics, Prometheus output, an OpenAPI specification, and Swagger UI.

## How it works

1. An application calls `GET /suggest?query=<prefix>`.
2. The active project's hot prefix tries are checked in RAM.
3. A hot hit returns immediately from the trie. A cold miss uses the sorted, disk-backed corpus index.
4. Repeated demand promotes the precise prefix into the bounded hot cache.
5. Metrics expose latency, hot/cold state, promotion activity, and cache hit rate.

The current release is a single-node service. Projects are isolated by corpus, cache, versions, usage data, and source-file history.

## Repository layout

```text
cmd/
  server/       HTTP service entry point
  bootstrap/    corpus bootstrap utility
internal/
  engine/       query path and hot/cold selection
  cache/        bounded in-memory prefix cache
  corpus/       versioned sorted corpus and index reader
  server/       console, API, Swagger, and metrics
docs/           OpenAPI, architecture, and visual assets
Dockerfile      container image
docker-compose.yml
```

## Quick start (Windows / PowerShell)

### 1. Clone and configure

```powershell
git clone https://github.com/mayankdwivediDS/bedrock-search.git
Set-Location bedrock-search
Copy-Item .env.example .env
```

Edit `.env` before exposing the service outside a trusted environment. In particular, set a strong `ADMIN_TOKEN`.

### 2. Validate and build

```powershell
go test ./...
go build -o bin\neo-server.exe .\cmd\server
go build -o bin\neo-bootstrap.exe .\cmd\bootstrap
```

### 3. Prepare a corpus

The server needs a corpus under the configured `DATA_DIR`. Bootstrap one from a JSON array of search terms:

```powershell
.\bin\neo-bootstrap.exe -source .\keywords.json -data .\data -list default -version v1
```

### 4. Run the service

```powershell
.\bin\neo-server.exe
```

Open these local URLs:

- Console: `http://localhost:8001/console`
- Swagger UI: `http://localhost:8001/docs`
- OpenAPI: `http://localhost:8001/openapi.yaml`
- Health: `http://localhost:8001/health`

## Docker deployment

With Docker Desktop running:

```powershell
docker compose up --build
```

The Compose setup persists service data in a volume. Configure environment values in `.env` or your deployment environment.

### Public deployment for judges

Deploy this repository to any Docker-capable server (a small Linux VM, Render,
Railway, Fly.io, or an AWS container service). Set the environment values in the
host's secret/environment-variable settings rather than committing a `.env`
file:

```text
ADMIN_TOKEN=<a new random value with at least 32 characters>
CORS_ORIGINS=https://<your-public-domain>
SWAGGER_PROTECT=false
```

On a Linux VM with Docker installed, clone the repository, create the three
environment variables above in a local `.env` file, and run:

```bash
docker compose up -d --build
```

Put the service behind an HTTPS reverse proxy (such as Caddy or Nginx) and share
`https://<your-public-domain>/console` with judges. Give the admin token only in
the private Devpost “additional info” field; never put it in the repository or
on the public project page. The public read-only endpoints include `/health`,
`/suggest`, `/metrics/json`, and `/docs`.

## API examples

```text
GET /suggest?query=app
GET /suggest?query=appliaction&fuzzy=true
GET /metrics/json
GET /health
```

Suggestions identify their source as `hot` or `cold`, making cache behaviour visible during integration and operations.

## Operations console

The console supports project creation and deletion, CSV file-set imports, corpus reloads, hot/cold prefix inspection, cache metrics, and API documentation. Each project has its own data directory and lifecycle.

## Technology

- Go and Fiber for the service API
- Adaptive in-memory prefix tries for hot paths
- Disk-backed sorted corpus with a skip index for cold paths
- CSV ingestion and versioned corpus lifecycle
- Prometheus-compatible and JSON metrics
- Docker for repeatable deployment

## Built with Codex and GPT-5.6

Codex with GPT-5.6 was used as an implementation partner throughout the project:
to help design the hot/cold search architecture, implement and refine the Go
service and operations console, add tests and deployment assets, and review the
developer documentation and hackathon demo materials. The final design,
integration decisions, testing, and submission are owned and verified by the
project author.

## Verification

```powershell
go test ./...
go build ./...
```
