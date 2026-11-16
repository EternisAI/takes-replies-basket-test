# Source provenance

This standalone test service is derived from the Axion backend sources at PR #1948 head snapshot `efc6b814cf4e5657ef4e539e4325c1e1d0784e29` and carries the narrowly relevant fixes from PR #2010 snapshot `ccf800f4203953b68e8cec926e00ea0b694ab828`. See [CHANGELOG.md](CHANGELOG.md) for the standalone adaptations, compatibility differences, and known issues.

The extracted reply state, revision handling, idempotency, conversation prompts, decision/evidence enrichment, and durable proposal worker retain their source behavior. The standalone PostgreSQL tables and HTTP API are adapters for this test deployment. The default basket mode is the source's `EXPLORATORY_EXPRESSION_V1`; optional `DEEP_RESEARCH_V1` is excluded and will be rejected. No license is asserted here. The repository owner must set the publication license separately.

PR #2010 carryovers are limited to UTF-8 rune-count validation for expression text limits and hiding a terminally failed revision's Build choice from the returned conversation. No unrelated later snapshots or production deployment code are included.
