package buddy

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

// ---- Reference model -------------------------------------------------------
//
// The reference allocator is an independent implementation: the heap is an
// explicit binary tree (1-indexed heap layout) plus a byte-occupancy grid.
// Node 1 is the root (order k); children of i are 2i (low half) and 2i+1.
//
// State per node:
//
//	nodeFree  — one contiguous free block covering the node's region
//	nodeSplit — children describe the region
//	nodeAlloc — one live allocation covers the region exactly
const (
	nodeFree = iota
	nodeSplit
	nodeAlloc
)

type refModel struct {
	k     int
	cap   int
	state []uint8 // len 2^(k+1), index 0 unused
	owner []string
	bytes []bool // per-byte occupancy, for non-overlap / conservation checks
	reqsz map[string]int
}

func newRef(k int) *refModel {
	m := &refModel{
		k:     k,
		cap:   1 << k,
		state: make([]uint8, 1<<(k+1)),
		owner: make([]string, 1<<(k+1)),
		bytes: make([]bool, 1<<k),
		reqsz: map[string]int{},
	}
	m.state[1] = nodeFree
	return m
}

func orderOf(k, i int) int {
	d := 0
	for x := i; x > 1; x >>= 1 {
		d++
	}
	return k - d
}

func baseOf(k, i int) int {
	ord := orderOf(k, i)
	// In-order address: start is the node's offset; for aligned tree it is
	// (index among nodes of this depth) * 2^ord.
	depth := k - ord
	first := 1 << depth
	return (i - first) * (1 << ord)
}

// findFit implements the specified policy: the free node of the smallest
// order that can hold `need`, lowest address among ties. It scans the tree
// rather than descending blindly, because a leftmost-descending DFS would
// prefer a large low-address block over a smaller higher one.
func (m *refModel) findFit(i, need int) int {
	best := -1
	bestOrder := m.k + 1
	bestStart := m.cap
	var walk func(int)
	walk = func(i int) {
		if i >= len(m.state) {
			return
		}
		switch m.state[i] {
		case nodeFree:
			o := orderOf(m.k, i)
			if o < need {
				return
			}
			s := baseOf(m.k, i)
			if o < bestOrder || (o == bestOrder && s < bestStart) {
				best, bestOrder, bestStart = i, o, s
			}
			// No need to descend: children aren't individually free.
		case nodeSplit:
			walk(2 * i)
			walk(2*i + 1)
		}
	}
	walk(1)
	return best
}

func (m *refModel) alloc(id string, size int) (start, ord int, ok bool) {
	need := fitOrder(size)
	if need > m.k {
		return 0, 0, false
	}
	node := m.findFit(1, need)
	if node < 0 {
		return 0, 0, false
	}
	// Split from the free node down to order `need`, carving the low half.
	for orderOf(m.k, node) > need {
		m.state[node] = nodeSplit
		node = 2 * node // descend into the low half
	}
	m.state[node] = nodeAlloc
	m.owner[node] = id
	start = baseOf(m.k, node)
	ord = orderOf(m.k, node)
	for p := start; p < start+(1<<ord); p++ {
		if m.bytes[p] {
			panic("reference: overlapping allocation")
		}
		m.bytes[p] = true
	}
	m.reqsz[id] = size
	return start, ord, true
}

func (m *refModel) release(id string) (start, ord int, ok bool) {
	size := 1 << m.k
	node := 1
	for m.state[node] != nodeAlloc {
		switch m.state[node] {
		case nodeSplit:
			// descend toward the owner node: search children for the id
			if m.subtreeHas(2*node, id) {
				node = 2 * node
			} else {
				node = 2*node + 1
			}
			size /= 2
		default:
			return 0, 0, false
		}
	}
	if m.owner[node] != id {
		return 0, 0, false
	}
	start = baseOf(m.k, node)
	ord = orderOf(m.k, node)
	for p := start; p < start+(1<<ord); p++ {
		m.bytes[p] = false
	}
	m.owner[node] = ""
	m.state[node] = nodeFree
	// Merge while buddy is genuinely free AND of the same order.
	for node > 1 {
		parent := node / 2
		bro := node ^ 1
		if m.state[bro] != nodeFree || orderOf(m.k, bro) != orderOf(m.k, node) {
			break
		}
		m.state[parent] = nodeFree
		node = parent
	}
	delete(m.reqsz, id)
	return start, ord, true
}

func (m *refModel) subtreeHas(i int, id string) bool {
	if i >= len(m.state) {
		return false
	}
	if m.state[i] == nodeAlloc {
		return m.owner[i] == id
	}
	if m.state[i] == nodeSplit {
		return m.subtreeHas(2*i, id) || m.subtreeHas(2*i+1, id)
	}
	return false
}

// freeBlocks returns order -> sorted starts.
func (m *refModel) freeBlocks() map[int][]int {
	out := map[int][]int{}
	var walk func(int)
	walk = func(i int) {
		if i >= len(m.state) {
			return
		}
		switch m.state[i] {
		case nodeFree:
			o := orderOf(m.k, i)
			out[o] = append(out[o], baseOf(m.k, i))
		case nodeSplit:
			walk(2 * i)
			walk(2*i + 1)
		}
	}
	walk(1)
	return out
}

func (m *refModel) allocBlocks() map[string][2]int { // id -> {start, order}
	out := map[string][2]int{}
	var walk func(int)
	walk = func(i int) {
		if i >= len(m.state) {
			return
		}
		switch m.state[i] {
		case nodeAlloc:
			out[m.owner[i]] = [2]int{baseOf(m.k, i), orderOf(m.k, i)}
		case nodeSplit:
			walk(2 * i)
			walk(2*i + 1)
		}
	}
	walk(1)
	return out
}

// ---- helpers ---------------------------------------------------------------

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

func mustReplay(t *testing.T, k int, evs ...*Event) *ReplayResponse {
	t.Helper()
	c := 1 << k
	resp, err := Replay(&ReplayRequest{Capacity: &c, Events: evs})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	return resp
}

// assertAgainstReference drives both models with the same ops and checks
// per-event equivalence, plus byte-grid non-overlap and conservation.
func assertAgainstReference(t *testing.T, k int, req *ReplayRequest) {
	t.Helper()
	if err := Validate(req); err != nil {
		t.Fatalf("validate: %v", err)
	}
	resp, err := Replay(req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	ref := newRef(k)
	live := map[string]bool{}

	for idx, ev := range req.Events {
		snap := resp.Events[idx]
		switch ev.Type {
		case "allocate":
			s, o, ok := ref.alloc(*ev.AllocID, *ev.Size)
			if ok {
				live[*ev.AllocID] = true
				if snap.OOM {
					t.Fatalf("event %d: ref allocated but engine OOM", idx)
				}
				if snap.Block == nil || snap.Block.Start != s || snap.Block.Order != o {
					t.Fatalf("event %d: alloc mismatch ref=(%d,o%d) engine=%+v",
						idx, s, o, snap.Block)
				}
			} else {
				if !snap.OOM {
					t.Fatalf("event %d: ref OOM but engine allocated %+v", idx, snap.Block)
				}
			}
		case "free":
			s, o, ok := ref.release(*ev.AllocID)
			if !ok {
				t.Fatalf("event %d: ref could not free live id %q", idx, *ev.AllocID)
			}
			delete(live, *ev.AllocID)
			if snap.Block == nil || snap.Block.Start != s || snap.Block.Order != o {
				t.Fatalf("event %d: free mismatch ref=(%d,o%d) engine=%+v",
					idx, s, o, snap.Block)
			}
		}

		// Non-overlap on the byte grid.
		grid := make([]bool, 1<<k)
		for _, b := range snap.Allocated {
			for p := b.Start; p < b.End; p++ {
				if grid[p] {
					t.Fatalf("event %d: overlapping allocated byte %d", idx, p)
				}
				grid[p] = true
			}
		}
		// Every byte is either allocated or covered by exactly one free block.
		for d, blocks := range snap.Free {
			for _, b := range blocks {
				if b.Order != d {
					t.Fatalf("event %d: free block filed under wrong order", idx)
				}
				for p := b.Start; p < b.End; p++ {
					if grid[p] {
						t.Fatalf("event %d: free byte %d already marked", idx, p)
					}
					grid[p] = true
				}
			}
		}
		for p, used := range grid {
			if !used {
				t.Fatalf("event %d: byte %d covered by neither alloc nor free", idx, p)
			}
		}

		// Conservation: allocated bytes + free bytes == capacity.
		allocBytes := 0
		for _, b := range snap.Allocated {
			allocBytes += b.Size
		}
		if allocBytes+snap.TotalFree != 1<<k {
			t.Fatalf("event %d: conservation broken %d+%d != %d",
				idx, allocBytes, snap.TotalFree, 1<<k)
		}

		// Equivalence with the reference tree.
		refFree := ref.freeBlocks()
		for d := 0; d <= k; d++ {
			got := []int{}
			for _, b := range snap.Free[d] {
				got = append(got, b.Start)
			}
			want := refFree[d]
			if want == nil {
				want = []int{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("event %d: free order %d mismatch engine=%v ref=%v",
					idx, d, got, want)
			}
		}
		refAlloc := ref.allocBlocks()
		if len(refAlloc) != len(snap.Allocated) {
			t.Fatalf("event %d: alloc count mismatch", idx)
		}
		for _, b := range snap.Allocated {
			r, ok := refAlloc[b.AllocID]
			if !ok || r[0] != b.Start || r[1] != b.Order {
				t.Fatalf("event %d: alloc %q mismatch", idx, b.AllocID)
			}
		}

		// Fragmentation metrics.
		var internal int
		for _, b := range snap.Allocated {
			internal += b.Size - b.RequestSize
		}
		if internal != snap.InternalFrag {
			t.Fatalf("event %d: internal frag %d want %d",
				idx, snap.InternalFrag, internal)
		}
		if snap.ExternalFrag != snap.TotalFree-snap.LargestFree {
			t.Fatalf("event %d: external frag formula broken", idx)
		}
	}
}

// ---- Fixed scenarios -------------------------------------------------------

func TestSplitLowHalfFirst(t *testing.T) {
	// k=4 (16B). A 1B request splits the 16B root along the low half, so
	// the first block is @0. The second 1B request takes the lowest o0 free
	// block, @1 — the high half carved by the first split.
	resp := mustReplay(t, 4,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("b"), Size: intptr(1)},
	)
	if resp.Events[0].Block.Start != 0 || resp.Events[0].Block.Order != 0 {
		t.Fatalf("first 1B alloc = %+v, want start 0 order 0", resp.Events[0].Block)
	}
	if resp.Events[1].Block.Start != 1 || resp.Events[1].Block.Order != 0 {
		t.Fatalf("second 1B alloc = %+v, want start 1 order 0", resp.Events[1].Block)
	}
	// After @0 and @1 held (o0): free blocks from the two split chains:
	// o0: none? @1 is held -> chain1: @1 held, @2-3 o1, @4-7 o2 ;
	// chain0: @0 held ... root still split with [8,16) free o3.
	s := resp.Events[1]
	want := map[int][]int{
		1: {2},
		2: {4},
		3: {8},
	}
	for d, starts := range want {
		var got []int
		for _, b := range s.Free[d] {
			got = append(got, b.Start)
		}
		if !reflect.DeepEqual(got, starts) {
			t.Fatalf("free order %d = %v, want %v (full=%v)", d, got, starts, s.Free)
		}
	}
}

func TestLowestAddressArbitration(t *testing.T) {
	// k=5 (32B). Fragment the heap, then prove:
	//  1) same order -> lowest address wins
	//  2) smaller fitting order is preferred over a larger lower block
	resp := mustReplay(t, 5,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("x"), Size: intptr(16)}, // [0,16)
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("y"), Size: intptr(8)},  // [16,24)
		&Event{EventID: "e3", Type: "allocate", AllocID: strptr("z"), Size: intptr(8)},  // [24,32)
		&Event{EventID: "e4", Type: "free", AllocID: strptr("y")},                       // free [16,24)
		&Event{EventID: "e5", Type: "free", AllocID: strptr("z")},                       // merge -> [16,32) o4
		&Event{EventID: "e6", Type: "allocate", AllocID: strptr("p"), Size: intptr(1)},  // low-half split of [16,32): @16
		&Event{EventID: "e7", Type: "allocate", AllocID: strptr("q"), Size: intptr(1)},  // @24
		&Event{EventID: "e8", Type: "free", AllocID: strptr("p")},                       // free 16 o0
		&Event{EventID: "e9", Type: "free", AllocID: strptr("x")},                       // free [0,16) o4
	)
	// After e9: free o0@16, o0@24(buddy? no, q holds 24), o4@0 plus the
	// high-half chain leftovers from p/q splitting: 17,18,20-23,25,26,28-31.
	// Allocate 1B: an o0 free exists (@16) -> must take it, NOT split [0,16).
	resp = mustReplay(t, 5,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("x"), Size: intptr(16)},
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("y"), Size: intptr(8)},
		&Event{EventID: "e3", Type: "allocate", AllocID: strptr("z"), Size: intptr(8)},
		&Event{EventID: "e4", Type: "free", AllocID: strptr("y")},
		&Event{EventID: "e5", Type: "free", AllocID: strptr("z")},
		&Event{EventID: "e6", Type: "allocate", AllocID: strptr("p"), Size: intptr(1)},
		&Event{EventID: "e7", Type: "allocate", AllocID: strptr("q"), Size: intptr(1)},
		&Event{EventID: "e8", Type: "free", AllocID: strptr("p")},
		&Event{EventID: "e9", Type: "free", AllocID: strptr("x")},
		&Event{EventID: "e10", Type: "allocate", AllocID: strptr("r"), Size: intptr(1)},
	)
	if b := resp.Events[9].Block; b.Start != 16 || b.Order != 0 {
		t.Fatalf("lowest-address/smallest-order arbitration failed: %+v", b)
	}
}

func TestMergeOnlyWithGenuinelyFreeSameOrderBuddy(t *testing.T) {
	// k=4. a@0(o0), b@1(o0) are buddies: freeing both merges to [0,2).
	// With c holding part of the region, no merge crosses it.
	resp := mustReplay(t, 4,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("b"), Size: intptr(1)},
		&Event{EventID: "e3", Type: "allocate", AllocID: strptr("c"), Size: intptr(1)}, // @2
		&Event{EventID: "e4", Type: "free", AllocID: strptr("a")},                      // o0 @0, buddy @1 held
		&Event{EventID: "e5", Type: "free", AllocID: strptr("b")},                      // now buddy @0 free same order -> merge [0,2)
	)
	var found bool
	for _, b := range resp.Events[4].Free[1] {
		if b.Start == 0 && b.End == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected merged free block [0,2) order 1, got %+v", resp.Events[4].Free[1])
	}
	// Full cascade: free c (@2 o0): buddy @0 is now order 1, not same order,
	// so @2 stays o0 until [0,2) also exists; then free 4-7 chain etc.
	resp2 := mustReplay(t, 4,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("b"), Size: intptr(1)},
		&Event{EventID: "e3", Type: "free", AllocID: strptr("a")},
		&Event{EventID: "e4", Type: "free", AllocID: strptr("b")}, // [0,2) o1
	)
	if len(resp2.Events[3].Free[4]) == 0 || resp2.Events[3].Free[4][0].Start != 0 {
		t.Fatalf("expected full cascade back to one 16B block, got %+v", resp2.Events[3].Free)
	}
}

func TestOOMIsValidAndStateUnchanged(t *testing.T) {
	// k=4: exhaust with sixteen 1-byte allocs... buddy fragmentation only
	// gives 16 o0 slots (capacity 16), so the 17th must OOM.
	var evs []*Event
	for i := 0; i < 16; i++ {
		id := string(rune('a' + i))
		evs = append(evs, &Event{EventID: id + "-ev", Type: "allocate",
			AllocID: strptr(id), Size: intptr(1)})
	}
	evs = append(evs, &Event{EventID: "oom", Type: "allocate",
		AllocID: strptr("zz"), Size: intptr(1)})
	resp := mustReplay(t, 4, evs...)
	oomSnap := resp.Events[16]
	if !oomSnap.OOM || oomSnap.Block != nil {
		t.Fatalf("expected OOM snapshot, got %+v", oomSnap)
	}
	if oomSnap.TotalFree != 0 || oomSnap.LargestFree != 0 {
		t.Fatalf("OOM snapshot should have no free memory, got total=%d", oomSnap.TotalFree)
	}
	// State after OOM equals state before OOM.
	before := resp.Events[15]
	if !reflect.DeepEqual(before.Allocated, oomSnap.Allocated) ||
		!reflect.DeepEqual(before.Free, oomSnap.Free) {
		t.Fatalf("state changed after OOM")
	}
	// Fragmentation-induced OOM: k=4 allocs of size 9 (order 4) twice.
	resp2 := mustReplay(t, 4,
		&Event{EventID: "1", Type: "allocate", AllocID: strptr("big1"), Size: intptr(9)},
		&Event{EventID: "2", Type: "allocate", AllocID: strptr("big2"), Size: intptr(1)},
	)
	if !resp2.Events[1].OOM {
		t.Fatalf("9-byte alloc must consume the whole 16B heap; second alloc should OOM")
	}
}

func TestExternalFragmentationDetected(t *testing.T) {
	// Two free 8B buddies that cannot merge (because ... ) — simplest: k=4,
	// allocate 8 (@0 o3), allocate 4 (@8 o2), free 8 -> o3 @0 free,
	// free the 4 -> o2 @8; total free 12 but largest 8, external frag 4.
	resp := mustReplay(t, 4,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(8)},
		&Event{EventID: "e2", Type: "allocate", AllocID: strptr("b"), Size: intptr(4)},
		&Event{EventID: "e3", Type: "free", AllocID: strptr("a")},
		&Event{EventID: "e4", Type: "free", AllocID: strptr("b")},
	)
	// After both frees: buddies o3@0 and o3@8 merge to full heap.
	if resp.Events[3].TotalFree != 16 || resp.Events[3].LargestFree != 16 ||
		resp.Events[3].ExternalFrag != 0 {
		t.Fatalf("expected fully coalesced heap, got %+v", resp.Events[3])
	}
	// Mid-state (after e3): free o3@0 (8B) + o2@12 (4B high half of [8,16)
	// which was split for b@8): total 12, largest 8, external 4.
	s := resp.Events[2]
	if s.TotalFree != 12 || s.LargestFree != 8 || s.ExternalFrag != 4 {
		t.Fatalf("external frag not captured: total=%d largest=%d ext=%d",
			s.TotalFree, s.LargestFree, s.ExternalFrag)
	}
}

func TestStructuralErrorsRejectWholeBatch(t *testing.T) {
	cases := []struct {
		name string
		req  *ReplayRequest
	}{
		{"missing capacity", &ReplayRequest{Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"capacity not power of two", &ReplayRequest{Capacity: intptr(100), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"capacity out of range", &ReplayRequest{Capacity: intptr(8), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"no events", &ReplayRequest{Capacity: intptr(16)}},
		{"duplicate event id", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
			{EventID: "e", Type: "free", AllocID: strptr("a")}}}},
		{"duplicate alloc id", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
			{EventID: "e2", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"free unknown id", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "free", AllocID: strptr("ghost")}}}},
		{"double free", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
			{EventID: "e2", Type: "free", AllocID: strptr("a")},
			{EventID: "e3", Type: "free", AllocID: strptr("a")}}}},
		{"size zero", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(0)}}}},
		{"size too big", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(17)}}}},
		{"unknown type", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "realloc", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"missing size", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a")}}}},
		{"free with size", &ReplayRequest{Capacity: intptr(16), Events: []*Event{
			{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(1)},
			{EventID: "e2", Type: "free", AllocID: strptr("a"), Size: intptr(1)}}}},
		{"reuse allocId after OOM still violates global uniqueness", &ReplayRequest{
			Capacity: intptr(16),
			Events: []*Event{
				{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(16)},
				{EventID: "e2", Type: "allocate", AllocID: strptr("b"), Size: intptr(1)}, // OOM
				{EventID: "e3", Type: "allocate", AllocID: strptr("b"), Size: intptr(1)}, // id reused
			}}},
		{"301 events", func() *ReplayRequest {
			evs := []*Event{}
			for i := 0; i < 301; i++ {
				evs = append(evs, &Event{EventID: "e" + strconv.Itoa(i),
					Type: "allocate", AllocID: strptr("a" + strconv.Itoa(i)), Size: intptr(1)})
			}
			return &ReplayRequest{Capacity: intptr(16), Events: evs}
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.req); err == nil {
				t.Fatalf("expected validation error")
			}
			if _, err := Replay(tc.req); err == nil {
				t.Fatalf("expected replay error")
			}
		})
	}
}

func TestRandomOpsMatchReference(t *testing.T) {
	seed := timeNow()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d", seed)
	for iter := 0; iter < 60; iter++ {
		k := 4 + rng.Intn(5) // 4..8: keeps the byte grid cheap
		cap := 1 << k
		// Build the batch against a probe reference so that only ids of
		// allocations that actually succeeded end up in the free pool.
		probe := newRef(k)
		var evs []*Event
		var pool []string
		used := map[string]bool{}
		n := 20 + rng.Intn(80)
		for i := 0; i < n && len(evs) < 300; i++ {
			eid := idFor(seed, iter, i)
			if len(pool) > 0 && rng.Intn(100) < 40 {
				j := rng.Intn(len(pool))
				id := pool[j]
				pool = append(pool[:j], pool[j+1:]...)
				if _, _, ok := probe.release(id); !ok {
					t.Fatalf("probe: unexpected free failure of %q", id)
				}
				evs = append(evs, &Event{EventID: eid, Type: "free", AllocID: strptr(id)})
			} else {
				var id string
				for {
					id = "m" + idFor(seed, iter, len(evs)*7+rng.Intn(100000))
					if !used[id] {
						used[id] = true
						break
					}
				}
				size := 1 + rng.Intn(cap)
				ev := &Event{EventID: eid, Type: "allocate", AllocID: strptr(id), Size: intptr(size)}
				if _, _, ok := probe.alloc(id, size); ok {
					pool = append(pool, id)
				}
				evs = append(evs, ev)
			}
		}
		req := &ReplayRequest{Capacity: intptr(cap), Events: evs}
		t.Run("batch", func(t *testing.T) {
			assertAgainstReference(t, k, req)
		})
	}
}

func TestJSONShape(t *testing.T) {
	c := 16
	resp := mustReplay(t, 4,
		&Event{EventID: "e1", Type: "allocate", AllocID: strptr("a"), Size: intptr(3)},
	)
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["capacity"] != float64(c) || got["k"] != float64(4) {
		t.Fatalf("bad envelope: %s", raw)
	}
	snap := got["events"].([]any)[0].(map[string]any)
	if snap["eventId"] != "e1" || snap["type"] != "allocate" || snap["oom"] != false {
		t.Fatalf("bad snapshot: %v", snap)
	}
	blk := snap["block"].(map[string]any)
	if blk["start"] != float64(0) || blk["order"] != float64(2) || blk["size"] != float64(4) {
		t.Fatalf("3-byte request should round to order 2: %v", blk)
	}
	if snap["internalFragmentation"] != float64(1) {
		t.Fatalf("internal frag should be 1: %v", snap)
	}
}
