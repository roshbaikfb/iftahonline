import { defineConfig, devices } from '@playwright/test';

// Smoke test: the real Go API binary (PLATFORM_BIN, built by `make build`)
// plus the production dashboard build served by `vite preview`.
// The API needs no live database for /v1/meta/version (pools are lazy).
const apiPort = 18180;
const webPort = 4173;
const platformBin = process.env.PLATFORM_BIN ?? '../../bin/platform';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: { baseURL: `http://127.0.0.1:${String(webPort)}`, trace: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: `${JSON.stringify(platformBin)} api`, // quoted: repo paths may contain spaces
      url: `http://127.0.0.1:${String(apiPort)}/livez`,
      reuseExistingServer: false,
      timeout: 10_000,
      env: {
        APP_ENV: 'test',
        HTTP_ADDR: `127.0.0.1:${String(apiPort)}`,
        DATABASE_URL: 'postgres://none:none@127.0.0.1:1/none',
        REDIS_URL: 'redis://127.0.0.1:1/0',
        // Public development-only values (see .env.example), not secrets.
        SECRETBOX_KEYS: '1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=',
        OTP_PEPPER: 'ZGV2LW9ubHktb3RwLXBlcHBlci1ub3QtYS1zZWNyZXQ=',
        DRAIN_DELAY: '0s',
      },
    },
    {
      command: `npm run build && npx vite preview --host 127.0.0.1 --port ${String(webPort)}`,
      url: `http://127.0.0.1:${String(webPort)}`,
      reuseExistingServer: false,
      timeout: 120_000,
      env: { VITE_API_PROXY: `http://127.0.0.1:${String(apiPort)}` },
    },
  ],
});
