# Bedrock Search

> **The bedrock autocomplete engine — drop it under any search system.**

Bedrock Search is a plug-and-play autocomplete engine that grounds any search
bar in real intent — instantly suggesting relevant keywords from millions of
entries as users type, so no product has to build search-as-you-type from
scratch.

## Planned technology stack

- **Go 1.23** for the high-performance API service.
- **Fiber** for HTTP routing and middleware.
- **Disk-backed sorted corpus and adaptive trie cache** for low-latency prefix
  suggestions at scale.
- **CSV ingestion** for bringing a product's own searchable terms into the
  engine.
- **OpenAI GPT-5.6** for optional, schema-validated suggestion humanization
  and intent labels, with a deterministic non-AI fallback.
- **Docker** for repeatable local and cloud deployment.
- **Go test** for unit and integration coverage.

## Build status

This repository is being built for OpenAI Build Week. The delivery plan,
quality gates, and provenance approach are in [PLAN.md](PLAN.md).

## What we are building

Users will enter a partial query and receive fast, relevant completions. A
humanization layer will optionally group and label those results so the next
search feels useful rather than mechanical. The first release will also support
CSV-based corpus management, feedback capture, health checks, and a lightweight
demo interface.

## Development

Implementation has not started yet. Once the service is initialized, the
standard validation command will be:

```powershell
go test ./...
```

## Provenance

This is a new product workspace. A prior autocomplete service was reviewed as
technical reference; any authorized reuse will be identified clearly, while
the Build Week work and use of Codex/GPT-5.6 will be documented in the final
submission.
