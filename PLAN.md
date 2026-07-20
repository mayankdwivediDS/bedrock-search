# Build Plan — Humanized Search Suggestions

## Purpose

Build a hackathon-ready search-suggestion tool that helps people refine a short
or vague query into useful, human-readable next searches. The tool will be a
new product built in this workspace. It may use ideas or reusable components
from the existing `go-suggest-neo` project only where permitted, with clear
provenance recorded in the submission.

## Reference reviewed

The reference project is a Go autocomplete API with a disk-backed corpus,
adaptive hot/cold trie cache, CSV ingestion, blacklist support, metrics,
backup/restore, and Swagger documentation. Its core value is fast prefix
retrieval. Our hackathon work will add a product layer that makes suggestions
contextual and understandable rather than merely returning prefix matches.

## Product scope

### User journey

1. A user enters a query such as `learn python`.
2. The service retrieves relevant prefix suggestions from its corpus.
3. A humanization layer groups, rewrites, and explains the suggestions, for
   example: *Start with fundamentals*, *Find a project to practise*, and
   *Compare learning paths*.
4. The user can select a suggestion, give feedback, and see better-ranked
   results on later searches.

### First-release features

- Search API returning ranked, human-readable suggestions.
- Corpus upload and validation from CSV.
- Deterministic ranking baseline: prefix quality, frequency, and diversity.
- Optional GPT-5.6-powered enrichment that turns raw suggestions into concise
  next-search prompts and labels their intent.
- Feedback endpoint (`helpful` / `not_helpful`) stored locally for evaluation.
- Lightweight browser UI for search, result groups, and feedback.
- Health endpoint, structured logs, and a repeatable demo dataset.

### Explicit non-goals for the first release

- Personal-data collection or user accounts.
- Claiming that model-generated suggestions are factual without verification.
- Production-scale multi-tenant infrastructure.

## Architecture

```text
Browser UI
    |
    v
Go API  ---> Prefix retrieval / ranked corpus
    |                 |
    |                 v
    |           CSV-backed local data
    |
    +---> Humanization adapter (GPT-5.6, with deterministic fallback)
    |
    +---> Feedback store + evaluation metrics
```

The retrieval layer will be kept behind an interface. That lets us evaluate a
fresh minimal implementation and any permitted reuse independently, while the
product API and humanization functionality remain new work in this repository.

## Delivery milestones

Each milestone ends with real tests passing and one descriptive commit. Commit
history must reflect the actual order of work; it is not a substitute for
provenance documentation.

| # | Deliverable | Verification | Commit theme |
|---|---|---|---|
| 0 | Repository setup, license, provenance note, and sample dataset | Formatting and test command run | `chore: initialize project and provenance` |
| 1 | Search API contract and deterministic prefix retrieval | Unit tests for normalization, limits, empty input, ranking | `feat: add ranked suggestion retrieval` |
| 2 | CSV ingestion with validation and a safe local corpus | Integration tests for upload and duplicate handling | `feat: add corpus ingestion` |
| 3 | Humanization adapter with deterministic fallback | Mocked adapter tests and prompt-schema validation | `feat: humanize search suggestions` |
| 4 | Feedback capture and evaluation counters | API tests and persisted-feedback tests | `feat: collect suggestion feedback` |
| 5 | Browser UI and end-to-end demo path | Manual smoke test plus API integration suite | `feat: add search experience` |
| 6 | Deployment configuration and demo materials | Clean-machine build, health check, and deployed smoke test | `chore: prepare deployment and demo` |

## Quality gates

Before each milestone is considered complete:

- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Exercise `/health` and the relevant API endpoint locally.
- Update this plan or the README when an API contract changes.
- Commit only the reviewed, working milestone.

Before submission:

- Repeat the full test suite from a clean checkout.
- Record a short demo using the supplied sample data.
- Verify the deployed URL and health check.
- Include a concise section stating what existed before Build Week, what was
  created during Build Week, and where Codex/GPT-5.6 was used.

## Acceptance criteria

The first release is ready when a reviewer can run it, upload a small CSV,
search a query, receive grouped human-readable suggestions even without an AI
key, submit feedback, and reproduce the test suite. With an OpenAI key
configured, the same request should show schema-validated GPT-5.6 enrichment
without exposing secrets or blocking the deterministic fallback.

## Open decisions to resolve before implementation

1. Hosting target: Docker on a simple web host is the default.
2. Data source and license for the initial suggestion corpus.
3. Whether the AI enrichment is enabled by default or demonstrated behind a
   visible toggle.
4. Which portions, if any, of the reference project are authorized for reuse.
