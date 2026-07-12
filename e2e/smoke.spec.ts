import { test, expect } from '@playwright/test';

test.describe('Lethe UI Smoke Tests', () => {
  test.beforeEach(async ({ page }) => {
    // Capture console errors
    page.on('console', msg => {
      if (msg.type() === 'error') {
        console.error(`Console error: ${msg.text()}`);
      }
    });
  });

  test('dashboard loads without errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', msg => {
      if (msg.type() === 'error') errors.push(msg.text());
    });

    await page.goto('/ui/dashboard');
    await expect(page.locator('nav .nav-logo')).toHaveText('Lethe');
    await expect(page.locator('.page')).toBeVisible();
    expect(errors).toHaveLength(0);
  });

  test('sessions page loads', async ({ page }) => {
    await page.goto('/ui/sessions');
    await expect(page.locator('nav')).toBeVisible();
    await expect(page.locator('.page')).toBeVisible();
  });

  test('flags page loads', async ({ page }) => {
    await page.goto('/ui/flags');
    await expect(page.locator('nav')).toBeVisible();
    await expect(page.locator('.page')).toBeVisible();
  });

  test('live page loads', async ({ page }) => {
    await page.goto('/ui/live');
    await expect(page.locator('nav')).toBeVisible();
    await expect(page.locator('.page')).toBeVisible();
  });
});
