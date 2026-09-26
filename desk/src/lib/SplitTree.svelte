<script>
  export let node;
  export let flashStart = -1;
  export let flashEnd = -1;
  export let depth = 0;

  $: covered = flashStart >= node.start && flashEnd <= node.end && flashStart !== -1;
  $: isLeaf = node.state !== 'split';
</script>

<div class="tree-node {node.state} {covered ? 'flash' : ''}" style="margin-left: {depth * 6}px">
  <div class="node-box">
    <span>{node.start}–{node.end}</span>
    <span class="muted">o{node.order} / {node.end - node.start}B</span>
    {#if node.state === 'alloc'}
      <span>🟧 {node.allocId} (req {node.requestSize})</span>
    {:else if node.state === 'free'}
      <span>🟩 free</span>
    {:else}
      <span> split</span>
    {/if}
  </div>
</div>

{#if node.state === 'split'}
  <div class="children" style="margin-left: {depth * 6 + 14}px">
    <div class="tree">
      <svelte:self node={node.low} {flashStart} {flashEnd} depth={0} />
    </div>
    <div class="tree">
      <svelte:self node={node.high} {flashStart} {flashEnd} depth={0} />
    </div>
  </div>
{/if}

<style>
  .muted {
    color: var(--muted);
    opacity: 0.85;
  }
</style>
