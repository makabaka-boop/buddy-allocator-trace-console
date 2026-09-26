package buddy

const (
	minK = 4
	maxK = 16
)

// liveAlloc records one currently held allocation.
type liveAlloc struct {
	id          string
	order       int
	start       int
	requestSize int
}

// freeSet stores the start addresses of the free blocks of one order.
// Addresses are aligned to the block size, so a map keys naturally and
// membership is O(1) — needed for buddy merge checks.
type freeSet map[int]struct{}

type engine struct {
	k         int
	capacity  int
	freelists []freeSet // index = order; order d blocks are 2^d bytes
	live      map[string]*liveAlloc
	allocsOK  int
	freesOK   int
	oomCount  int
}

func newEngine(k int) *engine {
	capacity := 1 << k
	e := &engine{
		k:         k,
		capacity:  capacity,
		freelists: make([]freeSet, k+1),
		live:      make(map[string]*liveAlloc),
	}
	for d := 0; d <= k; d++ {
		e.freelists[d] = freeSet{}
	}
	// One free block of the maximum order covering the whole heap.
	e.freelists[k][0] = struct{}{}
	return e
}

// fitOrder returns the smallest order whose blocks can hold size bytes.
func fitOrder(size int) int {
	d, n := 0, 1
	for n < size {
		n <<= 1
		d++
	}
	return d
}

// allocate rounds size up to an order, then splits from the smallest larger
// block that exists, always carving the low-address half. Among blocks of the
// requested order the lowest address is chosen. Returns false on OOM without
// mutating any state.
func (e *engine) allocate(id string, size int) (*liveAlloc, bool) {
	need := fitOrder(size)
	if need > e.k {
		return nil, false
	}

	// Find the smallest order >= need that currently has a free block.
	src := -1
	for d := need; d <= e.k; d++ {
		if len(e.freelists[d]) > 0 {
			src = d
			break
		}
	}
	if src < 0 {
		return nil, false // no block large enough: OOM, state unchanged
	}

	// Lowest address among that order's free blocks.
	start := e.capacity
	for a := range e.freelists[src] {
		if a < start {
			start = a
		}
	}
	delete(e.freelists[src], start)

	// Split down: each split consumes the parent and frees its HIGH half,
	// while the LOW half keeps being carved. This makes allocation prefer
	// the low-address half.
	for d := src; d > need; d-- {
		half := 1 << (d - 1)
		e.freelists[d-1][start+half] = struct{}{} // high half free
		// low half at `start` keeps being carved
	}
	// start now names the lowest-order block; its lower half chain was
	// never inserted as free, so the block at `start` of order `need` is
	// implicitly reserved.
	a := &liveAlloc{id: id, order: need, start: start, requestSize: size}
	e.live[id] = a
	e.allocsOK++
	return a, true
}

// free releases the block and merges upwards while the buddy is genuinely
// free AND of exactly the same order.
func (e *engine) free(id string) *liveAlloc {
	a := e.live[id]
	if a == nil {
		return nil
	}
	delete(e.live, id)

	order := a.order
	start := a.start
	for order < e.k {
		size := 1 << order
		// Buddy of a block is the adjacent block at the same alignment.
		buddy := start ^ size
		if _, ok := e.freelists[order][buddy]; !ok {
			break // buddy is allocated (or split): no merge
		}
		delete(e.freelists[order], buddy)
		start &= buddy // coalesce down to the pair's aligned base
		order++
	}
	e.freelists[order][start] = struct{}{}
	e.freesOK++
	return a
}
