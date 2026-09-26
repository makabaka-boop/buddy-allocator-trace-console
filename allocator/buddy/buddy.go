// Package buddy implements a classic binary-buddy memory allocator replay
// engine and the strict request validation surrounding it.
//
// Capacity is a power-of-two byte count (2^k, 4 <= k <= 16). Allocation
// rounds every request up to the smallest fitting order, picks the free
// block of that order with the lowest address and splits larger blocks
// down the low-address half first. Frees merge with the buddy only when it
// is genuinely free and of the same order.
package buddy

// Event is one operation in a replay batch. Pointer fields let validation
// distinguish "missing" from "explicitly null" in the decoded JSON.
type Event struct {
	EventID string  `json:"eventId"`
	Type    string  `json:"type"`
	AllocID *string `json:"allocId,omitempty"`
	Size    *int    `json:"size,omitempty"`
}

// ReplayRequest is the POST /api/replay payload.
type ReplayRequest struct {
	Capacity *int     `json:"capacity"`
	Events   []*Event `json:"events"`
}

// Block describes one allocated or free block in a snapshot.
type Block struct {
	Order       int    `json:"order"`
	Start       int    `json:"start"`
	End         int    `json:"end"` // exclusive
	Size        int    `json:"size"`
	AllocID     string `json:"allocId,omitempty"`     // allocated blocks only
	RequestSize int    `json:"requestSize,omitempty"` // allocated blocks only
}

// Snapshot is the allocator state after one event has been replayed.
type Snapshot struct {
	EventID      string     `json:"eventId"`
	Type         string     `json:"type"`
	OOM          bool       `json:"oom"`
	Block        *Block     `json:"block,omitempty"` // allocated (allocate) / freed (free) block; nil on OOM
	Allocated    []*Block   `json:"allocated"`
	Free         [][]*Block `json:"free"`                  // indexed by order
	InternalFrag int        `json:"internalFragmentation"` // allocated block size minus request size, in bytes
	TotalFree    int        `json:"totalFree"`
	LargestFree  int        `json:"largestFree"`
	ExternalFrag int        `json:"externalFragmentation"` // totalFree - largestFree, in bytes
}

// ReplayResponse is returned for a structurally valid batch.
type ReplayResponse struct {
	Capacity int         `json:"capacity"`
	K        int         `json:"k"`
	Events   []*Snapshot `json:"events"`
	TotalOps int         `json:"totalOps"`
	AllocsOK int         `json:"allocsOk"`
	FreesOK  int         `json:"freesOk"`
	OOMCount int         `json:"oomCount"`
}
