package buddy

// Replay validates and replays the batch, returning one snapshot per event.
// OOM is a valid event: the snapshot is marked oom and the state is unchanged.
// Structural errors must be caught by Validate before calling Replay.
func Replay(req *ReplayRequest) (*ReplayResponse, error) {
	if err := Validate(req); err != nil {
		return nil, err
	}
	k := 0
	for cap := *req.Capacity; cap > 1; cap >>= 1 {
		k++
	}
	e := newEngine(k)
	resp := &ReplayResponse{Capacity: *req.Capacity, K: k}

	for _, ev := range req.Events {
		snap := &Snapshot{EventID: ev.EventID, Type: ev.Type, Allocated: []*Block{}}

		switch ev.Type {
		case "allocate":
			a, ok := e.allocate(*ev.AllocID, *ev.Size)
			if !ok {
				// Valid OOM: record it, leave the allocator exactly as-is.
				snap.OOM = true
				e.oomCount++
			} else {
				snap.Block = liveBlock(a)
			}
		case "free":
			a := e.free(*ev.AllocID)
			snap.Block = liveBlock(a)
		}

		fillSnapshot(e, snap)
		resp.Events = append(resp.Events, snap)
	}

	resp.TotalOps = len(req.Events)
	resp.AllocsOK = e.allocsOK
	resp.FreesOK = e.freesOK
	resp.OOMCount = e.oomCount
	return resp, nil
}

func liveBlock(a *liveAlloc) *Block {
	if a == nil {
		return nil
	}
	size := 1 << a.order
	return &Block{
		Order:       a.order,
		Start:       a.start,
		End:         a.start + size,
		Size:        size,
		AllocID:     a.id,
		RequestSize: a.requestSize,
	}
}

func fillSnapshot(e *engine, s *Snapshot) {
	// Allocated blocks, deterministically ordered by start address.
	for _, a := range e.live {
		s.Allocated = append(s.Allocated, liveBlock(a))
	}
	// insertion sort: live count is small (<= 300)
	for i := 1; i < len(s.Allocated); i++ {
		for j := i; j > 0 && s.Allocated[j-1].Start > s.Allocated[j].Start; j-- {
			s.Allocated[j-1], s.Allocated[j] = s.Allocated[j], s.Allocated[j-1]
		}
	}

	s.Free = make([][]*Block, e.k+1)
	for d := 0; d <= e.k; d++ {
		s.Free[d] = []*Block{}
		for a := range e.freelists[d] {
			s.Free[d] = append(s.Free[d], &Block{
				Order: d,
				Start: a,
				End:   a + 1<<d,
				Size:  1 << d,
			})
		}
		// order by start
		for i := 1; i < len(s.Free[d]); i++ {
			for j := i; j > 0 && s.Free[d][j-1].Start > s.Free[d][j].Start; j-- {
				s.Free[d][j-1], s.Free[d][j] = s.Free[d][j], s.Free[d][j-1]
			}
		}
		s.TotalFree += len(e.freelists[d]) * (1 << d)
		if n := len(e.freelists[d]); n > 0 {
			if sz := 1 << d; sz > s.LargestFree {
				s.LargestFree = sz
			}
		}
	}

	for _, a := range e.live {
		s.InternalFrag += (1 << a.order) - a.requestSize
	}
	s.ExternalFrag = s.TotalFree - s.LargestFree
}
