package probe

import (
	"context"
	"encoding/json"
	"io"
)

// MaxResponseBytes caps how much of an API response body a prober reads, so a
// hostile, compromised, or misbehaving endpoint cannot stream an unbounded body
// and exhaust the headless timer's memory. It matches the Todoist client's cap
// from #2.
const MaxResponseBytes = 32 << 20

// DecodeJSON decodes r into v through a byte-capped reader.
func DecodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(r, MaxResponseBytes)).Decode(v)
}

// DrainClose reads and discards the rest of a response body (capped) before
// closing it, so the underlying connection can be reused for keep-alive.
func DrainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, MaxResponseBytes))
	_ = rc.Close()
}

// ReasonFromCtx classifies a transport failure: a tripped context deadline is a
// timeout, anything else is a generic probe error.
func ReasonFromCtx(ctx context.Context) Reason {
	if ctx.Err() != nil {
		return ReasonTimeout
	}
	return ReasonError
}
