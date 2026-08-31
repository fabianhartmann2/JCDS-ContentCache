package store

import (
	"context"
	"errors"
	"os"
	"sync"
)

type flight struct {
	mu            sync.Mutex
	ready         chan struct{}
	readyClosed   bool
	updated       chan struct{}
	done          chan struct{}
	pending       *Pending
	expectedBytes int64
	writtenBytes  int64
	finished      bool
	err           error
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
	active := &flight{
		ready:   make(chan struct{}),
		updated: make(chan struct{}),
		done:    make(chan struct{}),
	}
	f.entries[key] = active
	return &FlightHandle{parent: f, key: key, flight: active}, true
}

// Prepare publishes the growing temporary file to coordinated followers. It
// does not make the file visible in the final package namespace.
func (h *FlightHandle) Prepare(pending *Pending, expectedBytes int64) error {
	if pending == nil || expectedBytes < 0 {
		return errors.New("invalid in-flight package state")
	}
	h.flight.mu.Lock()
	defer h.flight.mu.Unlock()
	if h.flight.finished {
		return errors.New("package flight has already finished")
	}
	if h.flight.pending != nil {
		return errors.New("package flight has already been prepared")
	}
	h.flight.pending = pending
	h.flight.expectedBytes = expectedBytes
	h.closeReadyLocked()
	return nil
}

// Open waits until the leader has created the temporary file, then opens an
// independent reader. The reader remains valid if the file is atomically
// renamed into the final package namespace.
func (h *FlightHandle) Open(ctx context.Context) (*os.File, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-h.flight.ready:
	}

	h.flight.mu.Lock()
	pending := h.flight.pending
	expectedBytes := h.flight.expectedBytes
	err := h.flight.err
	h.flight.mu.Unlock()
	if err != nil {
		return nil, 0, err
	}
	if pending == nil {
		return nil, 0, errors.New("package flight ended before streaming started")
	}
	file, err := pending.OpenReader()
	if err != nil {
		return nil, 0, err
	}
	return file, expectedBytes, nil
}

// Advance notifies followers that bytes through writtenBytes are readable from
// the growing temporary file.
func (h *FlightHandle) Advance(writtenBytes int64) {
	h.flight.mu.Lock()
	defer h.flight.mu.Unlock()
	if h.flight.finished || writtenBytes <= h.flight.writtenBytes {
		return
	}
	h.flight.writtenBytes = writtenBytes
	close(h.flight.updated)
	h.flight.updated = make(chan struct{})
}

// WaitForBytes waits until more bytes are readable or the flight ends.
func (h *FlightHandle) WaitForBytes(ctx context.Context, offset int64) (writtenBytes int64, finished bool, err error) {
	for {
		h.flight.mu.Lock()
		writtenBytes = h.flight.writtenBytes
		finished = h.flight.finished
		err = h.flight.err
		updated := h.flight.updated
		h.flight.mu.Unlock()
		if writtenBytes > offset || finished {
			return writtenBytes, finished, err
		}
		select {
		case <-ctx.Done():
			return writtenBytes, false, ctx.Err()
		case <-updated:
		}
	}
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
	h.flight.mu.Lock()
	if h.flight.finished {
		h.flight.mu.Unlock()
		return
	}
	h.flight.err = err
	h.flight.finished = true
	h.closeReadyLocked()
	close(h.flight.updated)
	close(h.flight.done)
	h.flight.mu.Unlock()

	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()

	active, exists := h.parent.entries[h.key]
	if !exists || active != h.flight {
		return
	}
	delete(h.parent.entries, h.key)
}

func (h *FlightHandle) closeReadyLocked() {
	if h.flight.readyClosed {
		return
	}
	close(h.flight.ready)
	h.flight.readyClosed = true
}
