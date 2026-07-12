import { test, expect } from '@playwright/test';
import { randomUUID } from 'crypto';

test.describe('Lethe Assembly UI', () => {
  let sessionId: string;
  let eventId: string;

  test.beforeAll(async ({ request }) => {
    // Retry helper for server warmup
    async function retryPost(url: string, data: any, maxRetries = 3): Promise<any> {
      for (let i = 0; i < maxRetries; i++) {
        const res = await request.post(url, { data });
        if (res.ok()) return res;
        if (i < maxRetries - 1) {
          await new Promise(r => setTimeout(r, 500 * (i + 1)));
        } else {
          return res;
        }
      }
    }

    // Create a session via API
    const sessionRes = await retryPost('/api/sessions', {
      agent_id: 'test-agent',
      project_id: 'test-project',
      state: 'active',
    });
    if (!sessionRes.ok()) {
      console.error('Session creation failed:', sessionRes.status(), await sessionRes.text());
    }
    expect(sessionRes.ok()).toBeTruthy();
    const sessionData = await sessionRes.json();
    sessionId = sessionData.session_id;

    // Create an event
    const eventRes = await retryPost(`/api/sessions/${sessionId}/events`, {
      event_type: 'log',
      content: 'Test event for assembly',
    });
    if (!eventRes.ok()) {
      console.error('Event creation failed:', eventRes.status(), await eventRes.text());
    }
    expect(eventRes.ok()).toBeTruthy();
    const eventData = await eventRes.json();
    eventId = eventData.event_id;

    // Create an assembly
    const asmRes = await retryPost(`/api/sessions/${sessionId}/assemblies`, {
      assembly_id: `asm-${randomUUID()}`,
      source: 'test',
      assembler_version: '0.4.0',
      message_count: 5,
      packed_bytes: 1000,
      items: [
        {
          ordinal: 0,
          item_kind: 'summary',
          bucket: 'summary',
          content_snapshot: 'Test summary',
          content_sha256: 'abc123',
          packed_bytes: 800,
        },
        {
          ordinal: 1,
          item_kind: 'event',
          bucket: 'recent',
          event_id: eventId,
          content_sha256: 'def456',
          packed_bytes: 200,
        },
      ],
    });
    if (!asmRes.ok()) {
      console.error('Assembly creation failed:', asmRes.status(), await asmRes.text());
    }
    expect(asmRes.ok()).toBeTruthy();
  });

  test('session detail with assemblies tab', async ({ page }) => {
    await page.goto(`/ui/sessions/${sessionId}`);
    await expect(page.locator('.session-header')).toBeVisible();

    // Click assemblies tab
    await page.click('button.tab:has-text("Assemblies")');
    await expect(page.locator('#assemblies-pane')).toBeVisible();

    // Wait for HTMX to load assemblies list
    await page.waitForSelector('.assembly-row', { timeout: 10000 });

    // Check assembly content appears
    await expect(page.locator('.assembly-row')).toBeVisible();
  });

  test('assembly list links to detail page', async ({ page }) => {
    await page.goto(`/ui/sessions/${sessionId}`);
    await page.click('button.tab:has-text("Assemblies")');
    
    // Wait for HTMX to load assemblies list
    await page.waitForSelector('a:has-text("View detail →")', { timeout: 10000 });

    // Click the view detail link
    await page.click('a:has-text("View detail →")');

    // Should navigate to assembly detail
    await expect(page.locator('.session-label:has-text("Assembly")')).toBeVisible();
    await expect(page.locator('.assembly-detail')).toBeVisible();
  });

  test('assembly detail page loads', async ({ page }) => {
    // First get the assembly ID from the session
    await page.goto(`/ui/sessions/${sessionId}`);
    await page.click('button.tab:has-text("Assemblies")');
    
    // Wait for HTMX to load assemblies list
    await page.waitForSelector('a:has-text("View detail →")', { timeout: 10000 });

    // Get the assembly link and navigate to it
    const link = await page.locator('a:has-text("View detail →")').first();
    const href = await link.getAttribute('href');
    expect(href).toBeTruthy();

    await page.goto(href!);
    await expect(page.locator('.assembly-detail')).toBeVisible();
    await expect(page.locator('.assembly-items')).toBeVisible();
    await expect(page.locator('.assembly-totals')).toBeVisible();
    await expect(page.locator('.assembly-feedback-panel')).toBeVisible();
  });

  test('assembly feedback buttons work', async ({ page }) => {
    // Get assembly ID
    await page.goto(`/ui/sessions/${sessionId}`);
    await page.click('button.tab:has-text("Assemblies")');
    
    // Wait for HTMX to load assemblies list
    await page.waitForSelector('a:has-text("View detail →")', { timeout: 10000 });
    
    const link = await page.locator('a:has-text("View detail →")').first();
    const href = await link.getAttribute('href');

    await page.goto(href!);
    await page.click('button:has-text("✓ Good")');
    await page.waitForTimeout(500);

    // Check feedback note appears
    await expect(page.locator('.feedback-note')).toContainText('Feedback recorded');
  });
});
