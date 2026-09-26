// Build the binary split tree of the buddy heap from a replay snapshot.
// The heap is one max-order node; allocated and free blocks partition it
// completely. Anything not covered by a leaf block is a node that was split.
//
// Returns the root node:
//   { start, end, order, state: 'free'|'alloc'|'split', allocId, requestSize,
//     low, high }

export function buildTree(capacity, k, allocated, freeByOrder) {
  const allocByStart = new Map();
  for (const b of allocated || []) allocByStart.set(b.start, b);

  const freeByStart = new Map();
  for (const blocks of freeByOrder || []) {
    for (const b of blocks || []) freeByStart.set(b.start, b);
  }

  const mk = (start, order) => {
    const size = 1 << order;
    const node = { start, end: start + size, order, state: null };
    const a = allocByStart.get(start);
    if (a && a.order === order) {
      node.state = 'alloc';
      node.allocId = a.allocId;
      node.requestSize = a.requestSize;
      node.size = a.size;
      return node;
    }
    const f = freeByStart.get(start);
    if (f && f.order === order) {
      node.state = 'free';
      node.size = f.size;
      return node;
    }
    // Neither leaf covers this node: it was split into two buddies.
    node.state = 'split';
    node.low = mk(start, order - 1);
    node.high = mk(start + (size >> 1), order - 1);
    return node;
  };

  return mk(0, k);
}

// Merge allocated + free blocks into one ordered segment list for the
// address bar, giving each segment a deterministic key.
export function buildSegments(snapshot) {
  const segs = [];
  for (const b of snapshot.allocated || []) {
    segs.push({ ...b, state: 'alloc', key: 'a-' + b.start });
  }
  (snapshot.free || []).forEach((blocks, order) => {
    for (const b of blocks || []) {
      segs.push({ ...b, order, state: 'free', key: 'f-' + order + '-' + b.start });
    }
  });
  segs.sort((x, y) => x.start - y.start);
  return segs;
}
