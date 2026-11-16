package llm

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	ssestream "github.com/openai/openai-go/v3/packages/ssestream"
)

// FakeClient is a test double for Client. It records every call and replays
// caller-supplied chunks through a synthesized SSE decoder. Construct via
// NewFakeClient; queue stream responses with QueueStream.
//
// Lives in the production package (rather than a *_test.go file) so coordinator
// tests can import it without an internal/test/* dependency cycle.
type FakeClient struct {
	StreamResponses []FakeStreamResponse
	UnaryResponses  []FakeUnaryResponse

	StreamCalls []FakeStreamCall
	UnaryCalls  []FakeUnaryCall

	streamIdx int
	unaryIdx  int
}

// FakeStreamResponse describes one streamed reply: the chunks to yield in
// order, followed optionally by an error to surface via stream.Err().
type FakeStreamResponse struct {
	Chunks []openai.ChatCompletionChunk
	Err    error
}

// FakeUnaryResponse is the canned reply for a non-streamed call.
type FakeUnaryResponse struct {
	Resp *openai.ChatCompletion
	Err  error
}

// FakeStreamCall captures the parameters of one NewChatCompletionStream call.
type FakeStreamCall struct {
	Params openai.ChatCompletionNewParams
	Opts   []option.RequestOption
}

// FakeUnaryCall captures the parameters of one NewChatCompletion call.
type FakeUnaryCall struct {
	Params openai.ChatCompletionNewParams
	Opts   []option.RequestOption
}

func NewFakeClient() *FakeClient { return &FakeClient{} }

// QueueStream appends a streamed response to be returned by the next
// NewChatCompletionStream call. Calls are consumed in FIFO order.
func (f *FakeClient) QueueStream(resp FakeStreamResponse) {
	f.StreamResponses = append(f.StreamResponses, resp)
}

// QueueUnary appends a unary response to be returned by the next
// NewChatCompletion call.
func (f *FakeClient) QueueUnary(resp FakeUnaryResponse) {
	f.UnaryResponses = append(f.UnaryResponses, resp)
}

func (f *FakeClient) NewChatCompletion(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error) {
	f.UnaryCalls = append(f.UnaryCalls, FakeUnaryCall{Params: params, Opts: opts})
	if f.unaryIdx >= len(f.UnaryResponses) {
		return nil, nil
	}
	r := f.UnaryResponses[f.unaryIdx]
	f.unaryIdx++
	return r.Resp, r.Err
}

func (f *FakeClient) NewChatCompletionStream(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk] {
	f.StreamCalls = append(f.StreamCalls, FakeStreamCall{Params: params, Opts: opts})
	if f.streamIdx >= len(f.StreamResponses) {
		return ssestream.NewStream[openai.ChatCompletionChunk](newFakeDecoder(nil), nil)
	}
	r := f.StreamResponses[f.streamIdx]
	f.streamIdx++
	return ssestream.NewStream[openai.ChatCompletionChunk](newFakeDecoder(r.Chunks).withErr(r.Err), nil)
}

// fakeDecoder implements ssestream.Decoder by pre-encoding each chunk as a
// JSON SSE event and yielding them on Next/Event.
//
// Caveat: chunks constructed via Go struct literals leave the openai-go
// `respjson.Field` metadata at its zero value. The SDK's
// `ChatCompletionAccumulator.AddChunk` uses that metadata to detect
// FinishReason / content-finished transitions, so state-transition signals
// won't propagate through this fake the same way they do over a real wire.
// Content aggregation, stream errors, sink ordering, and param round-trip
// all work correctly — but if a test needs to assert on FinishReason or
// other respjson-gated fields, populate the chunk via `json.Unmarshal` from
// a raw JSON literal instead of a struct literal.
type fakeDecoder struct {
	events []ssestream.Event
	idx    int
	err    error
}

func newFakeDecoder(chunks []openai.ChatCompletionChunk) *fakeDecoder {
	d := &fakeDecoder{}
	for _, c := range chunks {
		// Prefer the original raw JSON when the chunk came from a real
		// unmarshal (e.g. via chunkFromJSON in tests). json.Marshal on the
		// struct alone loses ExtraFields like usage.cost — round-tripping
		// through the raw JSON preserves them.
		var data []byte
		if raw := c.RawJSON(); raw != "" {
			data = []byte(raw)
		} else {
			marshaled, err := json.Marshal(c)
			if err != nil {
				d.err = err
				break
			}
			data = marshaled
		}
		d.events = append(d.events, ssestream.Event{Type: "", Data: data})
	}
	return d
}

func (d *fakeDecoder) withErr(err error) *fakeDecoder {
	if err != nil {
		d.err = err
	}
	return d
}

func (d *fakeDecoder) Event() ssestream.Event {
	if d.idx == 0 || d.idx > len(d.events) {
		return ssestream.Event{}
	}
	return d.events[d.idx-1]
}

func (d *fakeDecoder) Next() bool {
	if d.idx >= len(d.events) {
		return false
	}
	d.idx++
	return true
}

func (d *fakeDecoder) Close() error { return nil }
func (d *fakeDecoder) Err() error   { return d.err }
