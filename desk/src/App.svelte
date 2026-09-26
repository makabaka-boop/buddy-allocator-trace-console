<script>
  import { onMount } from 'svelte';
  import SplitTree from './lib/SplitTree.svelte';
  import { buildTree, buildSegments } from './lib/tree.js';

  const DEFAULT_PAYLOAD = {
    capacity: 128,
    events: [
      { eventId: 'e1', type: 'allocate', allocId: 'a', size: 24 },
      { eventId: 'e2', type: 'allocate', allocId: 'b', size: 16 },
      { eventId: 'e3', type: 'allocate', allocId: 'c', size: 8 },
      { eventId: 'e4', type: 'free', allocId: 'b' },
      { eventId: 'e5', type: 'allocate', allocId: 'd', size: 9 },
      { eventId: 'e6', type: 'free', allocId: 'c' },
      { eventId: 'e7', type: 'allocate', allocId: 'e', size: 64 },
      { eventId: 'e8', type: 'free', allocId: 'd' },
      { eventId: 'e9', type: 'free', allocId: 'a' },
      { eventId: 'e10', type: 'free', allocId: 'e' }
    ]
  };

  let capacity = 128;
  let payloadText = JSON.stringify(DEFAULT_PAYLOAD, null, 2);
  let response = null;
  let error = '';
  let loading = false;
  let step = 0;

  const capacities = [];
  for (let k = 4; k <= 16; k++) capacities.push(1 << k);

  $: k = response ? response.k : Math.round(Math.log2(capacity));
  $: snapshot = response ? response.events[step] : null;
  $: tree = response && snapshot ? buildTree(response.capacity, response.k, snapshot.allocated, snapshot.free) : null;
  $: segments = snapshot ? buildSegments(snapshot) : [{ start: 0, end: capacity, state: 'free', key: 'init' }];
  $: flash = snapshot && snapshot.block
    ? { start: snapshot.block.start, end: snapshot.block.end }
    : { start: -1, end: -1 };

  async function replay() {
    error = '';
    loading = true;
    response = null;
    try {
      let payload;
      try {
        payload = JSON.parse(payloadText);
      } catch (e) {
        throw new Error('输入不是合法 JSON：' + e.message);
      }
      payload.capacity = Number(capacity);
      const res = await fetch('/api/replay', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const body = await res.json();
      if (!res.ok) {
        throw new Error(body.error || ('HTTP ' + res.status));
      }
      response = body;
      step = 0;
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function gotoStep(i) {
    if (!response) return;
    step = Math.max(0, Math.min(response.events.length - 1, i));
  }

  function eventLabel(ev) {
    if (ev.type === 'free') return 'free ' + ev.block?.allocId;
    const id = ev.block?.allocId;
    return 'allocate ' + (ev.oom ? 'OOM' : id + ' ' + ev.block?.requestSize + 'B→' + ev.block?.size + 'B');
  }

  onMount(replay);
</script>

<div class="app">
  <h1>Buddy Allocator Replay · 伙伴分配器回放</h1>

  <div class="layout">
    <div class="panel">
      <h2>输入</h2>
      <label class="field">
        总容量 2^k 字节（k = 4–16）
        <select bind:value={capacity}>
          {#each capacities as c}
            <option value={c}>{c} 字节（k={Math.round(Math.log2(c))}）</option>
          {/each}
        </select>
      </label>
      <label class="field">
        事件批次（eventId 唯一；allocate 带全局唯一 allocId 与 size；free 引用 allocId）
        <textarea bind:value={payloadText} spellcheck="false"></textarea>
      </label>
      <button class="btn" on:click={replay} disabled={loading} data-testid="replay">
        {loading ? '回放中…' : '回放 Replay'}
      </button>
      {#if error}<div class="error" data-testid="error">{error}</div>{/if}

      {#if response}
        <h2 style="margin-top: 16px">事件 {response.totalOps} · OOM {response.oomCount}</h2>
        <div class="event-list" data-testid="event-list">
          {#each response.events as ev, i}
            <div
              class="event-row {i === step ? 'active' : ''} {ev.oom ? 'oom' : ''}"
              on:click={() => gotoStep(i)}
              data-testid="event-row"
              data-index={i}
            >
              <span class="event-id">{ev.eventId}</span>
              <span class="event-type">{eventLabel(ev)}</span>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <div class="panel">
      {#if !response}
        <p class="muted">{error ? '整批被拒绝，无状态产生。' : '点击回放在此查看分裂树与地址条。'}</p>
      {:else}
        <div class="step-controls">
          <button class="btn secondary" on:click={() => gotoStep(0)} disabled={step === 0} data-testid="first">⏮</button>
          <button class="btn secondary" on:click={() => gotoStep(step - 1)} disabled={step === 0} data-testid="prev">◀ 上一步</button>
          <button class="btn secondary" on:click={() => gotoStep(step + 1)} disabled={step >= response.events.length - 1} data-testid="next">下一步 ▶</button>
          <button class="btn secondary" on:click={() => gotoStep(response.events.length - 1)} disabled={step >= response.events.length - 1} data-testid="last">⏭</button>
          <span class="step-info" data-testid="step-info">
            第 {step + 1} / {response.events.length} 步 · {snapshot.eventId} · {snapshot.type}
          </span>
        </div>

        {#if snapshot.oom}
          <div class="oom-banner" data-testid="oom-banner">
            OOM：没有能容纳请求的空块，状态与上一步完全相同（有效事件）。
          </div>
        {/if}

        <h2>地址条（{response.capacity}B）</h2>
        <div class="address-bar" data-testid="address-bar">
          {#each segments as seg}
            {@const on = seg.start >= flash.start && seg.end <= flash.end}
            <div
              class="seg {seg.state} {on ? 'flash' : ''}"
              style="flex-grow: {seg.end - seg.start}"
              title="[{seg.start},{seg.end}) order {seg.order} {seg.state === 'alloc' ? seg.allocId : 'free'}"
              data-testid="segment"
              data-state={seg.state}
            >
              {#if seg.end - seg.start >= response.capacity / 16}
                <span>{seg.state === 'alloc' ? seg.allocId : ''}</span>
              {/if}
            </div>
          {/each}
        </div>
        <div class="ruler"><span>0</span><span>{response.capacity / 2}</span><span>{response.capacity}</span></div>

        <div class="legend">
          <span><i class="alloc"></i>已分配</span>
          <span><i class="free"></i>空闲块</span>
          <span class="muted">高亮 = 本事件分配/释放的块</span>
        </div>

        <div class="stats" data-testid="stats">
          <div class="stat"><div class="k">总空闲</div><div class="v">{snapshot.totalFree}B</div></div>
          <div class="stat"><div class="k">最大空块</div><div class="v">{snapshot.largestFree}B</div></div>
          <div class="stat {snapshot.externalFragmentation > 0 ? 'warn' : ''}">
            <div class="k">外部碎片</div><div class="v" data-testid="ext-frag">{snapshot.externalFragmentation}B</div>
          </div>
          <div class="stat {snapshot.internalFragmentation > 0 ? 'warn' : ''}">
            <div class="k">内部碎片</div><div class="v" data-testid="int-frag">{snapshot.internalFragmentation}B</div>
          </div>
          <div class="stat"><div class="k">已分配块</div><div class="v">{snapshot.allocated.length}</div></div>
        </div>

        <h2>各阶空闲块</h2>
        <div class="order-table" data-testid="order-table">
          {#each snapshot.free as blocks, order}
            <span class="o">o{order} · {1 << order}B</span>
            <span class="order-runs">
              {#if blocks.length === 0}
                <span class="muted">—</span>
              {/if}
              {#each blocks as b}
                <span class="order-chip">[{b.start},{b.end})</span>
              {/each}
            </span>
            <span class="muted">{blocks.length} 块</span>
          {/each}
        </div>

        <h2 style="margin-top: 16px">分裂树</h2>
        <div class="tree-wrap" data-testid="tree">
          <div class="tree">
            <SplitTree node={tree} flashStart={flash.start} flashEnd={flash.end} />
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>
