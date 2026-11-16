# Proposal behavior retained from PR #1948

The worker consumes the durable reply-build queue. It saves the exact starting market catalogue before calling the builder, then stores the completion snapshot, proposal, assessment, and missing requirements. The immutable input records the owner, source revision, draft, ordered conversation messages, selected choice, construction mode, and hash. Request and revision deduplication prevent the same build from being submitted twice.

The default mode is server-owned `EXPLORATORY_EXPRESSION_V1`. It creates a conditional expression from the saved idea, confirms issuer identity against the SEC public ticker/exchange index, and maps only exact instruments in the Hyperliquid public catalogue. Inferred company connections and mix remain model inference. Listing membership does not establish user access or approval to trade. Unavailable mappings remain unresolved rather than being replaced with a different instrument.

The path does not run an earnings, filing, price, or news research package to establish the proposed companies. SEC data is used for identity matching. Model-produced business connections are not verified research findings. The output does not establish business fit, investability, expected return, optimized allocation, or performance.

`DEEP_RESEARCH_V1` is excluded and explicitly rejected. The copied legacy evidence-first/manual-review implementation is not part of this module. The worker makes no orders, trades, wallet operations, signing requests, or scheduled monitoring actions.

Only a saved Build choice can enqueue work. Enqueueing does not advance the reply revision. A worker crash may leave provider work uncertain, so an expired running build becomes `FAILED` and is never automatically replayed. A click on that same failed revision resolves to that failed job; a newer reply revision can be built again. The PR #2010 fix hides the failed revision's dead Build choice while leaving the durable failure visible.

The standalone host uses a fixed configured owner and a local no-charge admission adapter. OpenRouter usage rows are for inspection; this host does not bill or apply credits. Its PostgreSQL tables are isolated feature tables installed by the embedded migrations; they do not import Axion's user, wallet, trading, or production bootstrap schema.
