package store

import (
	"context"
	"sync"
)

type flight struct {
	done chan struct{}
	err  error
}

type Flights struct {
	mu      sync.Mutex
	entries map[string]*flight
}

func NewFlights() *Flights {
	return &Flights{entries: make(map[string]*flight)}
}

func (f *Flights) Join(key string) (*FlightHandle, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if active, exists := f.entries[key]; exists {
		return &FlightHandle{parent: f, key: key, flight: active}, false
	}
	active := &flight{done: make(chan struct{})}
	f.entries[key] = active
	return &FlightHandle{parent: f, key: key, flight: active}, true
}

type FlightHandle struct {
	parent *Flights
	key    string
	flight *flight
}

func (h *FlightHandle) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.flight.done:
		return h.flight.err
	}
}

func (h *FlightHandle) Finish(err error) {
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()

	active, exists := h.parent.entries[h.key]
	if !exists || active != h.flight {
		return
	}
	h.flight.err = err
	delete(h.parent.entries, h.key)
	close(h.flight.done)
}
