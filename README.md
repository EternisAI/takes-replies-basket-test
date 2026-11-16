# Takes replies + proposed basket test service

This repository runs the backend reply conversation and its durable, revision-bound **proposed basket** worker from PR #1948. The default basket path is `EXPLORATORY_EXPRESSION_V1`; the included PR #2010 changes cover Unicode character limits and failed-build choice handoff. Source hashes and extraction boundaries are in [PROVENANCE.md](PROVENANCE.md).

See [CHANGELOG.md](CHANGELOG.md) for the comparison with the original Takes backend, deployment history, live-test results, and issues that remain unfixed.

The service proposes conditional model inferences. It does not verify business fit, user account access, investability, performance, or an optimal allocation. A build does not place orders, trade, sign transactions, or start monitoring. There is no payment or credit adapter. Model usage is recorded for inspection only.

## Run locally

Requirements: Go 1.26.11 and PostgreSQL 17 or later.

```sh
cp .env.example .env
set -a
. ./.env
set +a
go run ./cmd/server
```

The server applies additive migrations at startup. It creates only its parent `threads`/`messages` tables and the feature's reply, build, and model-usage tables. It does not reset or drop any existing schema. Point `DATABASE_URL` at a fresh isolated test database for evaluation.

## Test

Run unit and adapter fixture checks with `go test ./...`. The PostgreSQL lifecycle tests use only `TAKES_REPLY_TEST_DATABASE_URL`, create uniquely named temporary schemas, and drop only those schemas. They do not fall back to `DATABASE_URL` and make no paid model calls. A PostgreSQL-backed run is:

```sh
TAKES_REPLY_TEST_DATABASE_URL='postgres://localhost:5432/takes_replies_test?sslmode=disable' go test ./...
```

The same command runs in GitHub Actions against a PostgreSQL 17 service.

`OPENROUTER_API_KEY` is optional for startup, health checks, creating conversations, and reading them. Without it, model-backed turns return `MODEL_NOT_CONFIGURED`; the worker cannot produce a basket. No canned response is substituted. When configured, the service calls OpenRouter using the source defaults `anthropic/claude-sonnet-5.5` for replies and `google/gemini-3.8-flash` for evidence and basket planning. Set `REPLY_MODEL`, `EVIDENCE_MODEL`, or `BASKET_MODEL` to override them.

The default public read-only market catalogue includes Hyperliquid native perpetuals and the configured `xyz` HIP-3 dex, matching the source backend default. Set `HYPERLIQUID_DEXES` to a comma-separated list. SEC issuer identity uses the public company ticker/exchange index. Neither provider adapter has account, wallet, order, or signing operations.

## API

`GET /healthz` is public and reports database/model configuration. All reply routes require `Authorization: Bearer $AUTH_BEARER_TOKEN`. The bearer token maps to one server-configured owner (`AUTH_USER_ID`, default `test-owner`); this test host does not provide multi-user identity.

Create a conversation with an idempotency key:

```sh
curl -sS -X POST http://localhost:8080/v1/reply-conversations \
  -H "Authorization: Bearer $AUTH_BEARER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"requestId":"a50c4a3e-e3bb-4b22-95df-e366c1c8138d"}'
```

Read it with `GET /v1/reply-conversations/{threadID}`. Send a turn with `POST /v1/reply-conversations/{threadID}/turns`:

```json
{
  "requestId": "44e518bc-f71c-4fd0-938f-b205cdd77438",
  "expectedRevision": 0,
  "message": "I want to express my view about data-center power demand."
}
```

The optional `selectedChoiceId` submits a saved conversation choice. Only a saved `build_now` choice queues a build; model text cannot enqueue one. Turns use optimistic revisions and persist request IDs so replayed requests do not call the model again.

## Build lifecycle

Build submissions save an immutable copy of the draft and ordered conversation messages, then enter the PostgreSQL queue. One worker in the server process claims jobs with a lease, persists a starting catalogue snapshot before provider work, and saves the result. A running job whose lease expires is marked failed rather than replayed, because the provider may already have run. A failed revision's Build choice is hidden; a later reply revision can submit a new build.

Build results have `QUEUED`, `RUNNING`, `COMPLETED`, `PARTIAL`, or `FAILED` status. The `EXPLORATORY_EXPRESSION_V1` path uses one bounded structured planner call plus read-only SEC identity and catalogue mapping. It does not run the optional legacy deep-research flow. The proposal is not a trade instruction or a verified investment recommendation.

## Configuration

See [.env.example](.env.example). Required values are `DATABASE_URL` and a strong, private `AUTH_BEARER_TOKEN`. Do not commit `.env`. `OPENROUTER_API_KEY` may be added as an Ink service secret after review; it is never read from a local workspace template or printed by this service.

The repository does not claim or assign a license. The repository owner must make that decision separately.
