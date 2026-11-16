package llmusage

import "time"

// TimePerOutputToken returns the mean seconds per output token over the decode
// window — total wall-clock minus time to first token, divided by output tokens.
// It is the OTel-canonical gen_ai.server.time_per_output_token primitive; invert
// it for tokens/sec. ok is false when there is no measurable window (a
// non-positive decode window or no output tokens), so callers skip the sample
// rather than record a bogus one.
//
// This is a CLIENT-OBSERVED rate. A provider that buffers then flushes tokens in
// a burst compresses the window and inflates a single sample; that noise is
// fundamental to client-side timing and is expected to wash out across the
// histogram. It is deliberately NOT "corrected" by folding TTFT back in or
// subtracting reasoning tokens — reasoning tokens are counted in output_tokens
// and, when the provider streams them, are generated within this window (TTFT is
// anchored to the first reasoning token upstream of this call).
func TimePerOutputToken(total, ttft time.Duration, outputTokens int64) (float64, bool) {
	decode := total - ttft
	if decode <= 0 || outputTokens <= 0 {
		return 0, false
	}
	return decode.Seconds() / float64(outputTokens), true
}
