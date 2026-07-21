# Bedrock Search — Judge Quick Start

This archive contains the runnable source code only. It deliberately excludes
local credentials, build outputs, caches, logs, and video-editing dependencies.

## Fastest local run

With Go installed, configure a corpus as described in the README, then run:

```powershell
go run .\cmd\server
```

Then visit `http://localhost:8001/console`.

## Docker run

1. Install Docker Desktop.
2. Copy `.env.example` to `.env` and replace `ADMIN_TOKEN` with a new random
   value of at least 32 characters.
3. Run `docker compose up --build`.
4. Visit `http://localhost:8001/console` and create/import a project using the
   console.

For a public deployment, use the Docker instructions in the repository README.
Keep the admin token private; it is only needed for write and administration
actions. Read-only endpoints work without it.

## Verification

With Go installed, run:

```text
go test ./...
go build ./...
```
