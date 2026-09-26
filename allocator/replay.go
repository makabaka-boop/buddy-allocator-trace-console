package main

import "fmt"

const (
	minOrder  = 4
	maxOrder  = 16
	maxEvents = 300
)

// Event is one allocate/free operation in a batch.
type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // "allocate" | "free"
	AllocID string `json:"allocId,omitempty"`
	Size    int    `json:"size,omitempty"`
}

type replayRequest struct {
	CapacityOrder int     `json:"capacityOrder"` // k, capacity = 1<<k bytes
	Events        []Event `json:"events"`
}

// eventResult is the outcome of one event plus the full state snapshot taken
// after it was applied (OOM events repeat the previous state).
type eventResult struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	AllocID   string `json:"allocId,omitempty"`
	Requested int    `json:"requested,omitempty"`
	Status    string `json:"status"` // "ok" | "oom"
	Block     *Block `json:"block,omitempty"`
	snapshot
}

type replayResponse struct {
	Capacity      int           `json:"capacity"`
	CapacityOrder int           `json:"capacityOrder"`
	Initial       snapshot      `json:"initial"`
	Events        []eventResult `json:"events"`
}

// apiError marks a structural error: the whole batch is rejected with 400.
type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

func errf(format string, args ...any) *apiError {
	return &apiError{msg: fmt.Sprintf(format, args...)}
}

// replay validates the batch and simulates it against a fresh allocator.
// Structural errors (bad shape, duplicate ids, freeing an unknown or already
// freed allocation, ...) reject the entire batch. An allocate that cannot be
// satisfied is a valid OOM event: it is recorded with status "oom" and the
// state carries on unchanged.
func replay(req replayRequest) (replayResponse, *apiError) {
	if req.CapacityOrder < minOrder || req.CapacityOrder > maxOrder {
		return replayResponse{}, errf("capacityOrder must be between %d and %d, got %d", minOrder, maxOrder, req.CapacityOrder)
	}
	if len(req.Events) > maxEvents {
		return replayResponse{}, errf("at most %d events allowed, got %d", maxEvents, len(req.Events))
	}

	a := newAllocator(req.CapacityOrder)
	res := replayResponse{
		Capacity:      a.capacity,
		CapacityOrder: req.CapacityOrder,
		Initial:       a.takeSnapshot(),
		Events:        []eventResult{},
	}
	seenEvents := make(map[string]bool)
	seenAllocs := make(map[string]bool) // allocation ids are globally unique across the batch

	for i, ev := range req.Events {
		if ev.ID == "" {
			return replayResponse{}, errf("events[%d]: id is required", i)
		}
		if seenEvents[ev.ID] {
			return replayResponse{}, errf("events[%d]: duplicate event id %q", i, ev.ID)
		}
		seenEvents[ev.ID] = true

		r := eventResult{ID: ev.ID, Type: ev.Type, AllocID: ev.AllocID}
		switch ev.Type {
		case "allocate":
			if ev.AllocID == "" {
				return replayResponse{}, errf("event %q: allocId is required for allocate", ev.ID)
			}
			if seenAllocs[ev.AllocID] {
				return replayResponse{}, errf("event %q: duplicate allocation id %q", ev.ID, ev.AllocID)
			}
			seenAllocs[ev.AllocID] = true
			if ev.Size < 1 || ev.Size > a.capacity {
				return replayResponse{}, errf("event %q: size must be between 1 and %d, got %d", ev.ID, a.capacity, ev.Size)
			}
			r.Requested = ev.Size
			if b, ok := a.allocate(ev.AllocID, ev.Size); ok {
				r.Status = "ok"
				r.Block = &b
			} else {
				r.Status = "oom"
			}
		case "free":
			if ev.AllocID == "" {
				return replayResponse{}, errf("event %q: allocId is required for free", ev.ID)
			}
			if !seenAllocs[ev.AllocID] {
				return replayResponse{}, errf("event %q: unknown allocation id %q", ev.ID, ev.AllocID)
			}
			if _, live := a.allocs[ev.AllocID]; !live {
				return replayResponse{}, errf("event %q: allocation %q is not live (already freed or never allocated)", ev.ID, ev.AllocID)
			}
			b := a.release(ev.AllocID)
			r.Status = "ok"
			r.Block = &b
		default:
			return replayResponse{}, errf("event %q: unknown type %q (want allocate|free)", ev.ID, ev.Type)
		}
		r.snapshot = a.takeSnapshot()
		res.Events = append(res.Events, r)
	}
	return res, nil
}
