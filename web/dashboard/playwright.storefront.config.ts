import { defineConfig, devices } from '@playwright/test';

// Storefront journeys on a phone (ADR-0019): the real Go storefront
// with a seeded demo store (internal/tools/sfdemo, built by `make
// storefront-e2e`), served at souq.localhost: Chromium resolves
// *.localhost to the loopback address, so the store's host routing is
// exercised for real.
const port = 18191;
const sfdemo = process.env.SFDEMO_BIN ?? '../../bin/sfdemo';

export default defineConfig({
  testDir: './e2e-storefront',
  timeout: 30_000,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report-storefront' }]] : 'list',
  use: { baseURL: `http://souq.localhost:${String(port)}`, trace: 'retain-on-failure' },
  projects: [{ name: 'mobile', use: { ...devices['Pixel 7'] } }],
  webServer: {
    command: `${JSON.stringify(sfdemo)} -addr 127.0.0.1:${String(port)}`, // quoted: repo paths may contain spaces
    url: `http://127.0.0.1:${String(port)}/livez`,
    reuseExistingServer: false,
    timeout: 180_000, // it starts PostgreSQL and Redis containers first
  },
});
