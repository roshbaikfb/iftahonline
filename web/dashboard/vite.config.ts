/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The API runs separately (make dev); /v1 is proxied so the dashboard and
// API share an origin, as they will behind the production reverse proxy.
const apiTarget = process.env.VITE_API_PROXY ?? 'http://127.0.0.1:18080';
const proxy = { '/v1': { target: apiTarget, changeOrigin: false } };

// The production build has no inline scripts, so preview (and production)
// can use a strict CSP. Dev mode is exempt: Vite injects inline HMR code.
const securityHeaders = {
  'Content-Security-Policy':
    "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'X-Frame-Options': 'DENY',
};

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, strictPort: true, proxy },
  preview: { port: 4173, strictPort: true, proxy, headers: securityHeaders },
  build: { sourcemap: true },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
});
