package encoding

import (
	"log/slog"
	"sync/atomic"
)

// ProgressSender provides a standard non-blocking send pattern for progress
// channels. Logs when updates are dropped due to a full channel.
type ProgressSender[T any] struct {
	ch      chan<- T
	dropped atomic.Int64
}

// NewProgressSender wraps a progress channel with standard send semantics.
// If ch is nil, all sends are no-ops.
func NewProgressSender[T any](ch chan<- T) *ProgressSender[T] {
	return &ProgressSender[T]{ch: ch}
}

// Send attempts to send a progress update. Non-blocking: if the channel
// is full, the update is dropped and a counter is incremented.
func (ps *ProgressSender[T]) Send(p T) {
	if ps.ch == nil {
		return
	}
	select {
	case ps.ch <- p:
	default:
		count := ps.dropped.Add(1)
		if count == 1 || count%100 == 0 {
			slog.Debug("progress update dropped (channel full)", "total_dropped", count)
		}
	}
}

// Dropped returns the total number of dropped progress updates.
func (ps *ProgressSender[T]) Dropped() int64 {
	return ps.dropped.Load()
}
