package ops

import (
	"sync/atomic"
	"time"
)

// Health tracks the last time the bot successfully reached Telegram, so
// /livez can answer something better than "the process is running".
//
// Why a probe and not "time since the last update": in nine of ten chats a
// quiet night is normal, so update traffic says nothing about liveness. A
// periodic GetMe travels the same path as every other call — the rate
// limiter, the dispatcher, the HTTP client — so it fails when that path is
// wedged, which is the failure a restart actually fixes.
//
// The window is deliberately wide (see LivenessWindow). A Telegram outage
// is NOT a reason to restart this pod: restarting cannot fix it and costs a
// full re-fetch of the ~4.9M-entry blocklist. The metric published
// alongside this is what surfaces a short outage; the probe only escalates
// to a restart once the process has been unable to reach Telegram for long
// enough that being stuck is the better explanation.
type Health struct {
	lastOK atomic.Int64 // unix nanos
	window time.Duration
}

// NewHealth returns a Health considered live for window after each Beat.
// The clock starts now, so a slow start does not report unhealthy before
// the first probe has had a chance to run.
func NewHealth(window time.Duration, now time.Time) *Health {
	h := &Health{window: window}
	h.lastOK.Store(now.UnixNano())
	return h
}

// Beat records a successful round trip to Telegram.
func (h *Health) Beat(now time.Time) { h.lastOK.Store(now.UnixNano()) }

// Age reports how long ago the last successful round trip was.
func (h *Health) Age(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, h.lastOK.Load()))
}

// Live reports whether the last successful round trip is within the window.
func (h *Health) Live(now time.Time) bool { return h.Age(now) <= h.window }
