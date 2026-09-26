import { test, expect } from '@playwright/test';

// End-to-end main flow against the real Go backend (proxied by Vite):
// allocate -> see it on the bar/tree -> free -> block becomes free and
// merges -> allocate again and reuse the reclaimed address.
test.describe('buddy replay main flow', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('step-info')).toBeVisible();
  });

  test('replays default batch step by step: allocate, free, reuse', async ({ page }) => {
    const info = page.getByTestId('step-info');

    // Step 1: allocate a (24B -> 32B block) at address 0.
    await expect(info).toContainText('第 1 / 10 步');
    await expect(info).toContainText('e1');
    const stats0 = page.getByTestId('stats');
    await expect(stats0).toContainText('32B'); // block size
    await expect(page.getByTestId('int-frag')).toHaveText('8B'); // 32-24
    const allocSegs = page.locator('[data-testid="segment"][data-state="alloc"]');
    await expect(allocSegs).toHaveCount(1);

    // Advance through e2, e3 (two more allocations).
    await page.getByTestId('next').click();
    await expect(info).toContainText('e2');
    await page.getByTestId('next').click();
    await expect(info).toContainText('e3');
    await expect(allocSegs).toHaveCount(3);

    // e4: free b (16B @32). One fewer allocated segment, free block appears.
    await page.getByTestId('next').click();
    await expect(info).toContainText('e4');
    await expect(allocSegs).toHaveCount(2);
    const extFragAfterFree = page.getByTestId('ext-frag');
    // Total free is 16+free leftovers but largest is >= 64 (region [64,128)
    // is still free o6); external fragmentation is > 0.
    await expect(extFragAfterFree).not.toHaveText('0B');
    // The free block [32,48) must be visible in the per-order table.
    await expect(page.getByTestId('order-table')).toContainText('[32,48)');

    // e5: allocate d with size 9 -> rounds to 16B and REUSES [32,48).
    await page.getByTestId('next').click();
    await expect(info).toContainText('e5');
    await expect(allocSegs).toHaveCount(3);
    // Flashed segment is [32,48): the segment whose range starts at 32.
    const reused = page.locator('[data-testid="segment"].alloc.flash').first();
    await expect(reused).toHaveAttribute('title', expect.stringContaining('[32,48)'));
    await expect(page.getByTestId('int-frag')).toContainText('15'); // 16-9 plus others

    // OOM step e7: allocate 64B while only the [64,128) region is contiguous
    // after c was freed — verify against actual data:
    // after e6 (free c, 8B @48 merges with [48,64) -> [32? no, d holds 32])
    // [48,64) is o4 free; [64,128) o6 free -> 64B fits, so e7 succeeds.
    await page.getByTestId('next').click(); // e6 free c
    await expect(info).toContainText('e6');
    await page.getByTestId('next').click(); // e7 allocate e 64
    await expect(info).toContainText('e7');
    await expect(allocSegs).toHaveCount(3); // a(32), d(16), e(64) = 112B

    // Free everything: full coalesce, zero fragmentation.
    await page.getByTestId('last').click();
    await expect(info).toContainText('e10');
    await expect(allocSegs).toHaveCount(0);
    await expect(page.locator('[data-testid="segment"][data-state="free"]')).toHaveCount(1);
    await expect(page.getByTestId('ext-frag')).toHaveText('0B');
    await expect(page.getByTestId('int-frag')).toHaveText('0B');
    await expect(page.getByTestId('stats')).toContainText('128B');
  });

  test('OOM event keeps state identical and is clearly flagged', async ({ page }) => {
    // Drive a custom batch: fill a 16B heap with a 9B alloc, then OOM on 1B.
    const payload = {
      capacity: 16,
      events: [
        { eventId: 'x1', type: 'allocate', allocId: 'big', size: 9 },
        { eventId: 'x2', type: 'allocate', allocId: 'small', size: 1 }
      ]
    };
    await page.evaluate((p) => {
      const ta = document.querySelector('textarea');
      ta.value = JSON.stringify(p, null, 2);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    }, payload);
    await page.selectOption('select', '16');
    await page.getByTestId('replay').click();

    await expect(page.getByTestId('step-info')).toContainText('x1');
    await page.getByTestId('next').click();
    await expect(page.getByTestId('step-info')).toContainText('x2');
    await expect(page.getByTestId('oom-banner')).toBeVisible();
    // Still exactly one allocated block, zero free bytes.
    await expect(page.locator('[data-testid="segment"][data-state="alloc"]')).toHaveCount(1);
    await expect(page.getByTestId('stats')).toContainText('0B');
  });

  test('structural error rejects the entire batch and shows the error', async ({ page }) => {
    const payload = {
      capacity: 16,
      events: [{ eventId: 'bad1', type: 'free', allocId: 'ghost' }]
    };
    await page.evaluate((p) => {
      const ta = document.querySelector('textarea');
      ta.value = JSON.stringify(p, null, 2);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    }, payload);
    await page.selectOption('select', '16');
    await page.getByTestId('replay').click();
    await expect(page.getByTestId('error')).toBeVisible();
    await expect(page.getByTestId('error')).toContainText('not a live allocation');
    await expect(page.getByTestId('step-info')).toHaveCount(0);
  });
});
