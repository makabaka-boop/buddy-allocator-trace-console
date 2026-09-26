package buddy

import "fmt"

// Validate checks the whole batch structurally. Replay must never be
// attempted on an invalid request: a structural error (unknown/free of an
// invalid id, duplicate ids, bad shape, ...) rejects the entire batch.
func Validate(req *ReplayRequest) error {
	if req == nil {
		return fmt.Errorf("request body is required")
	}
	if req.Capacity == nil {
		return fmt.Errorf("field 'capacity' is required")
	}
	cap := *req.Capacity
	if cap < 1<<minK || cap > 1<<maxK {
		return fmt.Errorf("capacity must be 2^k bytes with k in [%d,%d], i.e. %d..%d",
			minK, maxK, 1<<minK, 1<<maxK)
	}
	if cap&(cap-1) != 0 {
		return fmt.Errorf("capacity %d is not a power of two", cap)
	}
	if len(req.Events) == 0 {
		return fmt.Errorf("at least one event is required")
	}
	if len(req.Events) > 300 {
		return fmt.Errorf("at most 300 events are allowed, got %d", len(req.Events))
	}

	seenEvents := make(map[string]bool, len(req.Events))
	allocatedIDs := make(map[string]bool, len(req.Events)) // allocId ever used
	live := make(map[string]bool, len(req.Events))         // currently held
	for i, ev := range req.Events {
		at := func(msg string) error {
			id := ""
			if ev != nil {
				id = ev.EventID
			}
			if id != "" {
				return fmt.Errorf("event %d (eventId %q): %s", i, id, msg)
			}
			return fmt.Errorf("event %d: %s", i, msg)
		}
		if ev == nil {
			return at("event must not be null")
		}
		if ev.EventID == "" {
			return fmt.Errorf("event %d: eventId is required and must be non-empty", i)
		}
		if seenEvents[ev.EventID] {
			return at("duplicate eventId")
		}
		seenEvents[ev.EventID] = true

		switch ev.Type {
		case "allocate":
			if ev.AllocID == nil {
				return at("allocate requires allocId")
			}
			if *ev.AllocID == "" {
				return at("allocId must be non-empty")
			}
			if allocatedIDs[*ev.AllocID] {
				return at(fmt.Sprintf("allocId %q is not globally unique", *ev.AllocID))
			}
			if ev.Size == nil {
				return at("allocate requires size")
			}
			if *ev.Size < 1 || *ev.Size > cap {
				return at(fmt.Sprintf("size must be in [1, %d], got %d", cap, *ev.Size))
			}
			// Reserved only after the full batch validates; tracked here so a
			// later allocate cannot reuse the id even if an earlier allocate OOMs
			// (OOM keeps the id unused in terms of live memory, but the spec makes
			// allocId globally unique across operations — a repeat is still malformed).
			allocatedIDs[*ev.AllocID] = true
			live[*ev.AllocID] = true
		case "free":
			if ev.AllocID == nil || *ev.AllocID == "" {
				return at("free requires allocId")
			}
			if !live[*ev.AllocID] {
				if allocatedIDs[*ev.AllocID] {
					return at(fmt.Sprintf("allocId %q has already been freed", *ev.AllocID))
				}
				return at(fmt.Sprintf("allocId %q is not a live allocation", *ev.AllocID))
			}
			delete(live, *ev.AllocID)
			if ev.Size != nil {
				return at("free must not carry a size")
			}
		default:
			return at(fmt.Sprintf("unknown event type %q (want allocate or free)", ev.Type))
		}
	}
	return nil
}
