import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for Lethe UI end-to-end tests.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'html',
  use: {
    baseURL: 'http://127.0.0.1:28483',
    trace: 'on-first-retry',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: {
    command: 'go build ./cmd/lethe && ./lethe --db /tmp/lethe-test.db --http 127.0.0.1:28483',
    url: 'http://127.0.0.1:28483/api/health',
    reuseExistingServer: false,
    timeout: 120000,
  },
});
