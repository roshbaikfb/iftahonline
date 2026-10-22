import { defineConfig, devices } from '@playwright/test';

// The dashboard against the real API: dashdemo (internal/tools/dashdemo)
// starts throwaway PostgreSQL and Redis, runs the Go API binary
// (PLATFORM_BIN, built by `make build`) on them, seeds a merchant with
// orders through the API, and only then serves it on apiPort. The
// production dashboard build is served by `vite preview`, which proxies
// /v1 to it, as the reverse proxy does in production.
//
// The browser uses http://localhost: the session cookie is __Host- and
// Secure, which browsers accept on localhost without TLS.
const apiPort = 18180;
const webPort = 4173;
const platformBin = process.env.PLATFORM_BIN ?? '../../bin/platform';
const dashdemoBin = process.env.DASHDEMO_BIN ?? '../../bin/dashdemo';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: { baseURL: `http://localhost:${String(webPort)}`, trace: 'retain-on-failure', locale: 'ar-EG' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      // quoted: repo paths may contain spaces
      command: `${JSON.stringify(dashdemoBin)} -addr 127.0.0.1:${String(apiPort)} -platform ${JSON.stringify(platformBin)}`,
      url: `http://127.0.0.1:${String(apiPort)}/livez`,
      reuseExistingServer: false,
      timeout: 180_000,
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
