# Changelog

## 2026-11-16 — Standalone test backend and Ink deployment

This compares the standalone repository with the Axion/Takes backend used as its source, not with a separate FutrSocial backend or with the current upstream main branch.

- Base: [PR #1948](https://github.com/EternisAI/axionhypothesis/pull/1948), head `efc6b814cf4e5657ef4e539e4325c1e1d0784e29`.
- Scoped fixes: [PR #2010](https://github.com/EternisAI/axionhypothesis/pull/2010), head `ccf800f4203953b68e8cec926e00ea0b694ab828`.
- Initial standalone implementation: `26f21451098bb9a91bb81046d52f5a15bb17fd36`.
- The source repository was not modified. This is an extraction with new host adapters, not a byte-identical copy of either PR snapshot.

### Changes made for the standalone service

| Area | Original Takes backend | Standalone test backend |
| --- | --- | --- |
| Repository | Feature embedded in the Axion application | Separate public `EternisAI/takes-replies-basket-test` Go module with fresh Git history |
| API | Canonical GraphQL resolvers and shared application schema | REST create/read/continue endpoints and `/healthz`; existing GraphQL clients need an adapter |
| Authentication | Application authentication supplies the user identity | One bearer token maps to one configured test owner; this does not provide production multi-user authentication |
| Admission and accounting | Takes admission integrates credits, quotas, maintenance/pause checks, and broader application capacity controls | Minimal authenticated-owner admission plus a model-usage ledger; those production admission controls and billing are not reproduced. Core revision/request/build deduplication remains |
| Process layout | Separate server and worker entrypoints with shared application bootstrap | One server process also runs the durable PostgreSQL queue worker, with bounded graceful shutdown |
| Persistence | Reply migrations depend on evolved application `threads` and `messages` tables | Fresh database with minimal parent tables, reply migrations, usage table, and an embedded Goose migration runner |
| Generated storage models | Generated package includes unrelated application schema types | Retained only the model required by the reply queries |
| Basket construction | Runtime builder wires exploratory and legacy deep-research paths together | Separate exploratory builder behind planner, catalog, and company-identity interfaces; `DEEP_RESEARCH_V1` is rejected |
| Legacy snapshots | Absent-mode pre-release inputs can take the legacy path | Empty construction mode defaults to exploratory; legacy deep-research snapshot compatibility is not retained |
| Providers | Shared OpenRouter, market, and research infrastructure | Standalone OpenRouter adapter, read-only Hyperliquid catalog adapter, and SEC ticker-index identity reader; bounded metadata responses and redirects disabled |
| Model configuration | Shared application configuration | Environment variables retain the source reply/evidence/basket model defaults and allow overrides. Without a key, health/create/read work and model-backed turns fail explicitly |
| Packaging and operations | Application deployment and CI setup | Dockerfile, environment example, ignore files, setup/provenance documentation, and GitHub Actions tests using PostgreSQL 17 |

The extraction excludes the frontend, social publishing, trade execution, orders, wallet signing, monitoring, and the optional deep-research implementation. Wallet-secret detection remains as input validation; it is not a wallet integration. Shared pure validation helpers remain where required by the proposal contract.

### Fixes carried over from PR #2010

These fixes already existed upstream; they were ported into this extraction.

- Count Unicode code points rather than UTF-8 bytes when enforcing expression text limits, including optional assumption text.
- Hide the Build action for a terminally failed build on the current reply revision. A later revision can offer a new build; an older failed revision does not block it.
- Include regression coverage for the corresponding text-bound and failed-build behavior.

### Core behavior retained

- Reply prompts and structured contracts, decision/evidence enrichment, and saved conversation revisions.
- Owner-bound state, request idempotency, stale-revision checks, immutable build inputs, and revision-bound build deduplication.
- Durable queue claims and the rule that uncertain expired work is failed rather than automatically replayed.
- The default exploratory blueprint, company identity and contract mapping, explicit allocation constraints, and provenance labels.
- Unavailable exposure stays unassigned; surviving positions are not automatically enlarged to absorb it.
- The default source models: `anthropic/claude-sonnet-5.5` for replies and `google/gemini-3.8-flash` for evidence and basket planning.

The standalone SEC reader records retrieval time after validating and normalizing the index; the source recorded it immediately after JSON decoding. This small timestamp difference does not change the identity source.

### Deployment and configuration

- Created Ink project `takes-replies-basket-test` in workspace `eternis`.
- Provisioned a separate PostgreSQL 17 service, `replies-db`, with persistent storage.
- Deployed `takes-replies-api` from the public repository with bearer-protected conversation endpoints.
- Configured OpenRouter through the service environment after publication. Credentials were not committed to Git.
- Service endpoint: <https://takes-replies-api.us-east-1.gists.org>.

### Verification performed

- Unit and database-backed `go test ./...`, server compilation, and module consistency checks passed.
- Fresh SQL installation and the embedded migration runner passed in disposable database schemas.
- GitHub Actions passed for the initial implementation commit.
- Live read-only checks exercised the Hyperliquid catalog and SEC issuer index.
- Live requests verified replies, request replay, stale-revision rejection, failed-revision Build suppression, and completed-build deduplication.
- Four live basket attempts produced **two PARTIAL results, one FAILED result, and one COMPLETED result**. These were iterations of one test conversation, not an estimate of a production success rate.
- The completed test preserved NVIDIA 50%, Microsoft 50%, cash 0%, and a 365-day horizon. The output mapped available perpetual contracts; it did not establish stock ownership, account access, or investment performance. No trades were placed.

### Known issues discovered — not fixed in this change

- **Readiness mismatch:** the conversation can offer Build before the stricter scope/allocation checks needed for a complete basket pass.
- **Scope reconfirmation:** a nonempty free-text turn after selection marks the selected company scope as needing confirmation, even when it does not change the companies.
- **Inherited parsing restrictions:** natural-language allocations joined by `and`, conditional allocation qualifications, and the tested phrase `over the next 12 months` did not satisfy the builder's stricter instruction parsing. The successful input used semicolon-separated percentages and an explicit holding horizon, followed by scope selection.
- **Unexplained failed build:** one attempt failed after Gemini returned a completion. The generic stored failure and available Ink logs did not identify the internal cause. This is not established as a provider outage or an extraction regression.

Shared readiness/constraint handling, more flexible parsing, selective scope invalidation, and better retained error details are proposed follow-up work. The root-cause discussion did not implement those changes. See [PROVENANCE.md](PROVENANCE.md) for the extraction boundary and [README.md](README.md) for setup.
