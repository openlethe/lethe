import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for Lethe UI end-to-end tests.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  use: {
    baseURL: 'http://127.0.0.1:18483',
    trace: 'on-first-retry',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: {
    command: 'go build ./cmd/lethe && ./lethe --db :memory: --http 127.0.0.1:18483',
    url: 'http://127.0.0.1:18483/api/health',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
