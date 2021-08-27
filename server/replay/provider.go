package replay

import (
	"time"

	"github.com/tmaxmax/go-sse/server/event"
)

// A Provider implements an event replay pattern. The implementations are not required to be
// safe to use concurrently, so make sure only one goroutine writes to the provider (calls
// either Append or GC).
//
// See each provider's requirements for whether events should have IDs in order to be
// used with the respective provider.
type Provider interface {
	// Append puts the event in the replay buffer. If the provider also sets the event IDs
	// it swaps the given event with one that also has the new ID. It runs in O(1) amortized time.
	Append(**event.Event)
	// GC triggers a buffer cleanup. It runs in O(N) worst time (if all values are expired).
	// For some providers this might be a no-op, see their documentations.
	GC()
	// Range loops over the events that need to be replayed starting from the event after
	// the one with the specified ID.
	// It returns an error if the provided ID is invalid or doesn't exist.
	Range(from event.ID, fn func(*event.Event)) error
}

// Noop is a replay provider that does nothing. Use it when replaying events is not desired.
type Noop struct{}

func (Noop) Append(_ **event.Event)                       {}
func (Noop) GC()                                          {}
func (Noop) Range(_ event.ID, _ func(*event.Event)) error { return nil }

// NewFiniteProvider creates a replay Provider that can replay at maximum count event.
// The events' expiry times are not considered, as the oldest events are removed
// anyway when the provider has buffered the maximum number of events.
// The events must have an ID unless the provider is constructed with autoIDs flag as true.
func NewFiniteProvider(count int, autoIDs bool) *Finite {
	return &Finite{count: count, b: getBuffer(autoIDs, count)}
}

// NewValidProvider creates a replay Provider that replays all the buffered non-expired events.
// Call its GC method periodically to remove expired events from the buffer and release resources.
// You can use this provider for replaying an infinite number of events, if the events never
// expire.
// The events must have an ID unless the provider is constructed with autoIDs flag as true.
func NewValidProvider(autoIDs bool) *Valid {
	return &Valid{b: getBuffer(autoIDs, 0)}
}

// Finite is a replay provider that replays at maximum a certain number of events.
// GC is a no-op for this provider, as when the maximum number of values is reached
// and a new value has to be appended, old values are removed from the buffer.
type Finite struct {
	b     buffer
	count int
}

func (f *Finite) Append(ep **event.Event) {
	if f.b.len() == f.count {
		f.b.dequeue()
	}

	f.b.queue(ep)
}

func (f *Finite) GC() {}

func (f *Finite) Range(from event.ID, fn func(*event.Event)) error {
	events, err := f.b.slice(from)
	if err != nil {
		return err
	}

	for _, e := range events[1:] {
		fn(e)
	}

	return nil
}

// Valid is a replay provider that replays all the valid (not expired) previous events.
type Valid struct {
	b buffer
}

func (v *Valid) Append(ep **event.Event) {
	v.b.queue(ep)
}

func (v *Valid) GC() {
	now := time.Now()

	var e *event.Event
	for {
		e = v.b.front()
		if e == nil || e.ExpiresAt().Before(now) {
			break
		}
		v.b.dequeue()
	}
}

func (v *Valid) Range(from event.ID, fn func(*event.Event)) error {
	events, err := v.b.slice(from)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, e := range events[1:] {
		if e.ExpiresAt().After(now) {
			fn(e)
		}
	}

	return nil
}
